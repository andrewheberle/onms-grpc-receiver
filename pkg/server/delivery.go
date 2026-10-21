package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
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

// build returns the alertDelivery for the configuration
func (c deliveryConfig) build(logger *slog.Logger) (*alertDelivery, error) {
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

	return newAlertDelivery(discovery, c.headers, logger), nil
}

// alertDelivery sends alerts to every Alertmanager currently known
type alertDelivery struct {
	discovery alertmanagerDiscovery
	client    *http.Client
	logger    *slog.Logger

	// metrics
	total        *prometheus.CounterVec
	errors       *prometheus.CounterVec
	lookupErrors prometheus.Counter
}

// alertmanagerDiscovery finds the Alertmanagers to send alerts to
type alertmanagerDiscovery interface {
	// alertmanagers returns the base URL of every Alertmanager currently
	// known
	alertmanagers() ([]string, error)
}

// newAlertDelivery returns an alertDelivery that sends to the Alertmanagers
// found by discovery, adding headers to every request
func newAlertDelivery(discovery alertmanagerDiscovery, headers map[string]string, logger *slog.Logger) *alertDelivery {
	client := &http.Client{
		Timeout: time.Second * 5,
	}
	if headers != nil {
		client.Transport = &customTransport{
			Headers: headers,
		}
	}

	return &alertDelivery{
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
		lookupErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "onmsgrpc_alertmanager_lookup_error_total",
			Help: "Total number of errors during lookups of Alertmanagers.",
		}),
	}
}

// collectors returns the metrics of the alertDelivery for registration
func (d *alertDelivery) collectors() []prometheus.Collector {
	return []prometheus.Collector{d.total, d.errors, d.lookupErrors}
}

// send posts alerts to every Alertmanager currently known. An error is
// returned only if the Alertmanagers could not be found or the alerts could
// not be encoded; failures to send to an Alertmanager are logged and counted.
func (d *alertDelivery) send(alerts []*models.PostableAlert) error {
	if len(alerts) == 0 {
		return nil
	}

	ams, err := d.discovery.alertmanagers()
	if err != nil {
		d.lookupErrors.Inc()
		return err
	}

	payload, err := json.Marshal(alerts)
	if err != nil {
		return err
	}

	logger := d.logger.With("count", len(alerts))

	var wg sync.WaitGroup
	for _, am := range ams {
		wg.Add(1)
		go func(base string) {
			defer wg.Done()

			url, err := url.JoinPath(base, alertsPath)
			if err != nil {
				logger.Warn("error creating alertmanager url", "alertmanager", base, "error", err)
				d.total.WithLabelValues(base).Inc()
				d.errors.WithLabelValues(base).Inc()
				return
			}

			d.total.WithLabelValues(url).Inc()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				logger.Warn("error creating request", "url", url, "error", err)
				d.errors.WithLabelValues(url).Inc()
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := d.client.Do(req)
			if err != nil {
				logger.Warn("error sending to alertmanager", "url", url, "error", err)
				d.errors.WithLabelValues(url).Inc()
				return
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				logger.Warn("bad status code from alertmanager", "url", url, "status", resp.Status)
				d.errors.WithLabelValues(url).Inc()
				return
			}

			logger.Info("sent to alertmanager", "url", url, "status", resp.Status)
		}(am)
	}
	wg.Wait()

	return nil
}

// noDiscovery finds no Alertmanagers, so nothing is sent
type noDiscovery struct{}

func (noDiscovery) alertmanagers() ([]string, error) {
	return nil, nil
}

// staticDiscovery is a fixed list of Alertmanagers
type staticDiscovery []string

// newStaticDiscovery returns a staticDiscovery for the given base URLs,
// returning an error if any are invalid
func newStaticDiscovery(urls []string) (staticDiscovery, error) {
	for _, u := range urls {
		if _, err := url.JoinPath(u, alertsPath); err != nil {
			return nil, err
		}
	}

	return slices.Clone(urls), nil
}

func (d staticDiscovery) alertmanagers() ([]string, error) {
	return d, nil
}

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

	// now returns the current time
	now func() time.Time

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
		now: time.Now,
	}
}

func (d *srvDiscovery) alertmanagers() ([]string, error) {
	if d.ttl == 0 {
		return d.resolve()
	}

	d.mu.Lock()
	now := d.now()
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
	urls, err := d.resolve()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.store(urls)
	d.mu.Unlock()

	return urls, nil
}

// refresh looks up the SRV record again in the background
func (d *srvDiscovery) refresh() {
	urls, err := d.resolve()

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
	now := d.now()
	d.urls = urls
	d.expiresAt = now.Add(d.ttl)
	d.staleUntil = now.Add(d.ttl * 2)
	d.resolving = false
}

// resolve looks up the SRV record and returns the Alertmanager base URLs
func (d *srvDiscovery) resolve() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	addrs, err := d.lookup(ctx, d.name)
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		urls = append(urls, fmt.Sprintf("%s://%s", d.scheme, net.JoinHostPort(addr.Target, fmt.Sprint(addr.Port))))
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
