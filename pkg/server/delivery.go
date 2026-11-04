package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// alertsPath is the Alertmanager API path that alerts are posted to
const alertsPath = "/api/v2/alerts"

// discoveryKind is how Alertmanagers are found
type discoveryKind int

const (
	discoveryNone discoveryKind = iota
	discoveryStatic
	discoverySRV
)

// deliveryConfig holds the options for alert delivery until it is built
type deliveryConfig struct {
	discovery discoveryKind

	// Alertmanager base URLs for discoveryStatic
	urls []string

	// SRV record, URL scheme and cache TTL for discoverySRV
	srvName     string
	srvScheme   string
	srvCacheTTL time.Duration

	// headers added to every request
	headers map[string]string
}

// build returns the alertDelivery for the configuration, registering its
// metrics with reg
func (c deliveryConfig) build(logger *slog.Logger, reg prometheus.Registerer) (*alertDelivery, error) {
	var discovery alertmanagerDiscovery = noDiscovery{}

	switch c.discovery {
	case discoveryStatic:
		static, err := newStaticDiscovery(c.urls)
		if err != nil {
			return nil, err
		}
		discovery = static
	case discoverySRV:
		discovery = newSRVDiscovery(c.srvScheme, c.srvName, c.srvCacheTTL, logger)
	}

	return newAlertDelivery(discovery, c.headers, logger, reg), nil
}

// alertDelivery sends alerts to every Alertmanager currently known
type alertDelivery struct {
	discovery alertmanagerDiscovery
	client    *http.Client
	logger    *slog.Logger

	// metrics
	total        *prometheus.CounterVec
	errors       *prometheus.CounterVec
	retries      *prometheus.CounterVec
	lookupErrors prometheus.Counter
}

// alertmanagerDiscovery finds the Alertmanagers to send alerts to
type alertmanagerDiscovery interface {
	// alertmanagers returns the alerts URL of every Alertmanager currently
	// known, which the alerts are posted to
	alertmanagers(ctx context.Context) ([]string, error)
}

// The limits of a send, which are bounded as a single worker flushes alarms
// and heartbeats are sent from their gRPC stream. See ADR 0004.
const (
	// sendTimeout is how long a send may take, including every chunk and
	// retry for every Alertmanager
	sendTimeout = 10 * time.Second

	// requestTimeout is how long a single request to an Alertmanager may
	// take
	requestTimeout = 5 * time.Second

	// maxAttempts is how many requests are made for a chunk before giving
	// up on an Alertmanager
	maxAttempts = 3

	// chunkSize is the most alerts sent in a single request
	chunkSize = 1000

	// maxErrorBody is how much of the body of an error response from an
	// Alertmanager is included in the error
	maxErrorBody = 1024
)

// retryBackoff is how long to wait before each retry of a chunk
var retryBackoff = [maxAttempts - 1]time.Duration{500 * time.Millisecond, time.Second}

// newAlertDelivery returns an alertDelivery that sends to the Alertmanagers
// found by discovery, adding headers to every request, and registers its
// metrics with reg
func newAlertDelivery(discovery alertmanagerDiscovery, headers map[string]string, logger *slog.Logger, reg prometheus.Registerer) *alertDelivery {
	client := &http.Client{}
	if headers != nil {
		client.Transport = &customTransport{
			Headers: headers,
		}
	}

	d := &alertDelivery{
		discovery: discovery,
		client:    client,
		logger:    logger,
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "onmsgrpc_alertmanager_total",
			Help: "Total number of messages sent to alertmanager.",
		},
			[]string{"alertmanager"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "onmsgrpc_alertmanager_failed_total",
			Help: "Total number of messages that could not be sent to alertmanager.",
		},
			[]string{"alertmanager"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "onmsgrpc_alertmanager_retries_total",
			Help: "Total number of requests to alertmanager that were retried after a failure.",
		},
			[]string{"alertmanager"}),
		lookupErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "onmsgrpc_alertmanager_lookup_error_total",
			Help: "Total number of errors during lookups of Alertmanagers.",
		}),
	}
	reg.MustRegister(d.total, d.errors, d.retries, d.lookupErrors)

	return d
}

// send posts alerts to every Alertmanager currently known, in parallel, in
// chunks of up to chunkSize alerts. A chunk is retried after a network error
// or a response showing the Alertmanager may accept it later, and a chunk
// that fails stops the rest for that Alertmanager. The whole send takes at
// most sendTimeout.
//
// The error returned joins the failure of every Alertmanager that did not
// accept all the alerts, or reports that the Alertmanagers could not be found
// or the alerts could not be encoded.
func (d *alertDelivery) send(ctx context.Context, alerts []*postableAlert) error {
	if len(alerts) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	urls, err := d.discovery.alertmanagers(ctx)
	if err != nil {
		d.lookupErrors.Inc()
		return fmt.Errorf("finding alertmanagers: %w", err)
	}

	var chunks [][]byte
	for chunk := range slices.Chunk(alerts, chunkSize) {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return fmt.Errorf("encoding alerts: %w", err)
		}
		chunks = append(chunks, payload)
	}

	logger := d.logger.With("count", len(alerts))

	errs := make([]error, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Go(func() {
			errs[i] = d.post(ctx, u, chunks, logger)
		})
	}
	wg.Wait()

	return errors.Join(errs...)
}

// post sends every chunk to the Alertmanager alerts URL u in turn, counting
// the send and any failure
func (d *alertDelivery) post(ctx context.Context, u string, chunks [][]byte, logger *slog.Logger) error {
	d.total.WithLabelValues(u).Inc()

	for i, payload := range chunks {
		if err := d.postChunk(ctx, u, payload, logger); err != nil {
			d.errors.WithLabelValues(u).Inc()
			if len(chunks) > 1 {
				return fmt.Errorf("sending chunk %d of %d: %w", i+1, len(chunks), err)
			}

			return err
		}
	}

	return nil
}

// postChunk sends payload to the Alertmanager alerts URL u, retrying up to
// maxAttempts requests in all while the failure may be temporary
func (d *alertDelivery) postChunk(ctx context.Context, u string, payload []byte, logger *slog.Logger) error {
	for attempt := 1; ; attempt++ {
		err := d.do(ctx, u, payload, logger)
		if err == nil || attempt == maxAttempts || ctx.Err() != nil || !retryable(err) {
			return err
		}

		wait := retryBackoff[attempt-1]
		logger.Debug("retrying alertmanager", "url", u, "attempt", attempt+1, "wait", wait, "error", err)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w (gave up retrying: %w)", err, ctx.Err())
		case <-timer.C:
		}

		d.retries.WithLabelValues(u).Inc()
	}
}

// statusError is an unsuccessful response from an Alertmanager
type statusError struct {
	url    string
	code   int
	status string
	body   []byte
}

func (e *statusError) Error() string {
	if len(e.body) == 0 {
		return fmt.Sprintf("alertmanager %s returned %s", e.url, e.status)
	}

	return fmt.Sprintf("alertmanager %s returned %s: %s", e.url, e.status, e.body)
}

// retryable reports whether a request that failed with err may succeed if
// made again: network errors, server errors and rate limiting. Other
// responses, such as 400 Bad Request when some alerts are invalid, are not,
// as Alertmanager has already stored any valid alerts.
func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= http.StatusInternalServerError || se.code == http.StatusTooManyRequests
	}

	return true
}

// do makes a single request to post payload to the Alertmanager alerts URL u
func (d *alertDelivery) do(ctx context.Context, u string, payload []byte, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request for alertmanager %s: %w", u, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending to alertmanager: %w", err)
	}
	defer func() {
		// read what remains of the body so the connection can be reused
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

		return &statusError{url: u, code: resp.StatusCode, status: resp.Status, body: bytes.TrimSpace(body)}
	}

	logger.Info("sent to alertmanager", "url", u, "status", resp.Status)

	return nil
}

// noDiscovery finds no Alertmanagers, so nothing is sent
type noDiscovery struct{}

func (noDiscovery) alertmanagers(context.Context) ([]string, error) {
	return nil, nil
}

// staticDiscovery is a fixed list of Alertmanager alerts URLs
type staticDiscovery []string

// newStaticDiscovery returns a staticDiscovery for the given Alertmanager
// base URLs, returning an error if any are invalid
func newStaticDiscovery(urls []string) (staticDiscovery, error) {
	d := make(staticDiscovery, 0, len(urls))
	for _, u := range urls {
		alertsURL, err := url.JoinPath(u, alertsPath)
		if err != nil {
			return nil, fmt.Errorf("invalid alertmanager URL %q: %w", u, err)
		}
		d = append(d, alertsURL)
	}

	return d, nil
}

func (d staticDiscovery) alertmanagers(context.Context) ([]string, error) {
	return d, nil
}

// srvLookupTimeout is how long a lookup of the SRV record may take
const srvLookupTimeout = 2 * time.Second

// srvDiscovery finds Alertmanagers from a DNS SRV record. Results are cached
// for the TTL, then for up to another TTL the cached results are returned
// while they are looked up again in the background.
type srvDiscovery struct {
	name   string
	scheme string
	ttl    time.Duration
	logger *slog.Logger

	// lookup returns the SRV records for name
	lookup func(ctx context.Context, name string) ([]*net.SRV, error)

	mu         sync.Mutex
	urls       []string
	expiresAt  time.Time
	staleUntil time.Time
	resolving  bool
}

// newSRVDiscovery returns a srvDiscovery for the SRV record name, using
// scheme for the Alertmanager URLs. A ttl of zero disables caching.
func newSRVDiscovery(scheme, name string, ttl time.Duration, logger *slog.Logger) *srvDiscovery {
	resolver := new(net.Resolver)

	return &srvDiscovery{
		name:   name,
		scheme: scheme,
		ttl:    ttl,
		logger: logger,
		lookup: func(ctx context.Context, name string) ([]*net.SRV, error) {
			_, addrs, err := resolver.LookupSRV(ctx, "", "", name)

			return addrs, err
		},
	}
}

func (d *srvDiscovery) alertmanagers(ctx context.Context) ([]string, error) {
	if d.ttl == 0 {
		return d.resolve(ctx)
	}

	d.mu.Lock()
	now := time.Now()
	urls := d.urls
	switch {
	case now.Before(d.expiresAt):
		d.mu.Unlock()
		return urls, nil
	case now.Before(d.staleUntil):
		if !d.resolving {
			d.resolving = true
			go d.refresh()
		}
		d.mu.Unlock()
		return urls, nil
	}
	d.mu.Unlock()

	// cache expired beyond stale window, so resolve now
	urls, err := d.resolve(ctx)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.store(urls)
	d.mu.Unlock()

	return urls, nil
}

// refresh looks up the SRV record again in the background. The lookup is not
// tied to the send that started it, which may finish first, but is bounded
// by srvLookupTimeout.
func (d *srvDiscovery) refresh() {
	urls, err := d.resolve(context.Background())

	d.mu.Lock()
	defer d.mu.Unlock()

	d.resolving = false
	if err != nil {
		d.logger.Warn("background SRV re-resolve failed", "srv", d.name, "error", err)
		return
	}
	d.store(urls)
}

// store caches urls, the caller must hold mu
func (d *srvDiscovery) store(urls []string) {
	now := time.Now()
	d.urls = urls
	d.expiresAt = now.Add(d.ttl)
	d.staleUntil = now.Add(d.ttl * 2)
	d.resolving = false
}

// resolve looks up the SRV record and returns the Alertmanager alerts URLs,
// taking at most srvLookupTimeout
func (d *srvDiscovery) resolve(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, srvLookupTimeout)
	defer cancel()

	addrs, err := d.lookup(ctx, d.name)
	if err != nil {
		return nil, fmt.Errorf("looking up SRV record %s: %w", d.name, err)
	}

	urls := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		base := fmt.Sprintf("%s://%s", d.scheme, net.JoinHostPort(addr.Target, fmt.Sprint(addr.Port)))
		alertsURL, err := url.JoinPath(base, alertsPath)
		if err != nil {
			return nil, fmt.Errorf("building alertmanager URL from SRV record %s: %w", d.name, err)
		}
		urls = append(urls, alertsURL)
	}

	return urls, nil
}

type customTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	newReq := req.Clone(req.Context())
	if newReq.Header == nil {
		newReq.Header = make(http.Header)
	}
	for key, value := range t.Headers {
		newReq.Header.Set(key, value)
	}

	// Use the underlying transport to execute the request
	return t.transport().RoundTrip(newReq)
}

func (t *customTransport) transport() http.RoundTripper {
	if t.Transport != nil {
		return t.Transport
	}
	return http.DefaultTransport
}
