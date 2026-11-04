package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/client_golang/prometheus"
)

// recordingAlertmanager is a fake Alertmanager that records the requests it
// receives
type recordingAlertmanager struct {
	mu      sync.Mutex
	paths   []string
	headers []http.Header
	alerts  []models.PostableAlert
	status  int
	body    string
	*httptest.Server
}

func newRecordingAlertmanager(t *testing.T, status int) *recordingAlertmanager {
	t.Helper()

	am := &recordingAlertmanager{status: status}
	am.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var alerts []models.PostableAlert
		if err := json.NewDecoder(r.Body).Decode(&alerts); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}

		am.mu.Lock()
		am.paths = append(am.paths, r.URL.Path)
		am.headers = append(am.headers, r.Header)
		am.alerts = append(am.alerts, alerts...)
		am.mu.Unlock()

		w.WriteHeader(am.status)
		_, _ = w.Write([]byte(am.body))
	}))
	t.Cleanup(am.Close)

	return am
}

func testAlerts() []*postableAlert {
	return []*postableAlert{{
		Labels: map[string]string{"alertname": "test"},
	}}
}

// discoveryFunc adapts a function to alertmanagerDiscovery
type discoveryFunc func() ([]string, error)

func (f discoveryFunc) alertmanagers(context.Context) ([]string, error) {
	return f()
}

// newTestStaticDiscovery returns a staticDiscovery for the Alertmanager base
// URLs, failing the test if any are invalid
func newTestStaticDiscovery(t *testing.T, urls ...string) staticDiscovery {
	t.Helper()

	d, err := newStaticDiscovery(urls)
	if err != nil {
		t.Fatalf("newStaticDiscovery() error = %v", err)
	}

	return d
}

func TestAlertDeliverySend(t *testing.T) {
	am1 := newRecordingAlertmanager(t, http.StatusOK)
	am2 := newRecordingAlertmanager(t, http.StatusOK)

	reg := prometheus.NewRegistry()
	d := newAlertDelivery(newTestStaticDiscovery(t, am1.URL, am2.URL+"/prefix"), map[string]string{"X-Scope-OrgID": "noc"}, slog.New(slog.DiscardHandler), reg)

	if err := d.send(context.Background(), testAlerts()); err != nil {
		t.Fatalf("send() error = %v", err)
	}

	for _, tt := range []struct {
		am   *recordingAlertmanager
		path string
	}{
		{am1, "/api/v2/alerts"},
		{am2, "/prefix/api/v2/alerts"},
	} {
		if !reflect.DeepEqual(tt.am.paths, []string{tt.path}) {
			t.Errorf("paths = %v, want %v", tt.am.paths, []string{tt.path})
		}
		if len(tt.am.alerts) != 1 || tt.am.alerts[0].Labels["alertname"] != "test" {
			t.Errorf("alerts = %v, want the test alert", tt.am.alerts)
		}
		if got := tt.am.headers[0].Get("X-Scope-OrgID"); got != "noc" {
			t.Errorf("X-Scope-OrgID header = %q, want %q", got, "noc")
		}
		if got := tt.am.headers[0].Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type header = %q, want %q", got, "application/json")
		}
		if got := metricValue(t, reg, "onmsgrpc_alertmanager_total", "alertmanager", tt.am.URL+tt.path); got != 1 {
			t.Errorf("total for %s = %v, want 1", tt.path, got)
		}
	}
}

// TestAlertDeliverySendFailure checks a failure of one Alertmanager is
// returned and counted, including what the Alertmanager said, while the
// others are still sent the alerts
func TestAlertDeliverySendFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{"server error", http.StatusInternalServerError, "", []string{"500 Internal Server Error"}},
		{"invalid alerts", http.StatusBadRequest, "start time must be before end time\n", []string{"400 Bad Request", "start time must be before end time"}},
		{"long body", http.StatusBadRequest, strings.Repeat("x", maxErrorBody*2), []string{"400 Bad Request: " + strings.Repeat("x", maxErrorBody)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := newRecordingAlertmanager(t, http.StatusOK)
			failing := newRecordingAlertmanager(t, tt.status)
			failing.body = tt.body

			reg := prometheus.NewRegistry()
			d := newAlertDelivery(newTestStaticDiscovery(t, ok.URL, failing.URL), nil, slog.New(slog.DiscardHandler), reg)

			err := d.send(context.Background(), testAlerts())
			if err == nil {
				t.Fatal("send() error = nil, want the failing alertmanager's error")
			}
			for _, want := range append(tt.want, failing.URL+alertsPath) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("send() error = %q, want containing %q", err, want)
				}
			}
			if strings.Contains(err.Error(), ok.URL+alertsPath) {
				t.Errorf("send() error = %q, want no mention of the working alertmanager", err)
			}
			if strings.Contains(err.Error(), strings.Repeat("x", maxErrorBody+1)) {
				t.Errorf("send() error has %d bytes, want the body cut to %d bytes", len(err.Error()), maxErrorBody)
			}

			if len(ok.alerts) != 1 {
				t.Errorf("working alertmanager got %d alerts, want 1", len(ok.alerts))
			}
			if got := metricValue(t, reg, "onmsgrpc_alertmanager_failed_total", "alertmanager", failing.URL+alertsPath); got != 1 {
				t.Errorf("errors for failing alertmanager = %v, want 1", got)
			}
			if got := metricValue(t, reg, "onmsgrpc_alertmanager_failed_total", "alertmanager", ok.URL+alertsPath); got != 0 {
				t.Errorf("errors for working alertmanager = %v, want 0", got)
			}
		})
	}
}

// TestAlertDeliverySendCancelled checks a send stops when its context is
// cancelled, and is counted as a failure
func TestAlertDeliverySendCancelled(t *testing.T) {
	am := newRecordingAlertmanager(t, http.StatusOK)

	reg := prometheus.NewRegistry()
	d := newAlertDelivery(newTestStaticDiscovery(t, am.URL), nil, slog.New(slog.DiscardHandler), reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := d.send(ctx, testAlerts()); !errors.Is(err, context.Canceled) {
		t.Errorf("send() error = %v, want context.Canceled", err)
	}
	if len(am.alerts) != 0 {
		t.Errorf("alertmanager got %d alerts, want none", len(am.alerts))
	}
	if got := metricValue(t, reg, "onmsgrpc_alertmanager_failed_total", "alertmanager", am.URL+alertsPath); got != 1 {
		t.Errorf("errors = %v, want 1", got)
	}
}

func TestAlertDeliverySendLookupError(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := newAlertDelivery(discoveryFunc(func() ([]string, error) {
		return nil, errors.New("lookup failed")
	}), nil, slog.New(slog.DiscardHandler), reg)

	if err := d.send(context.Background(), testAlerts()); err == nil {
		t.Error("send() error = nil, want lookup error")
	}
	if got := metricValue(t, reg, "onmsgrpc_alertmanager_lookup_error_total"); got != 1 {
		t.Errorf("lookupErrors = %v, want 1", got)
	}
}

func TestAlertDeliverySendNothing(t *testing.T) {
	looked := false
	d := newAlertDelivery(discoveryFunc(func() ([]string, error) {
		looked = true
		return nil, nil
	}), nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())

	if err := d.send(context.Background(), nil); err != nil {
		t.Errorf("send() error = %v", err)
	}
	if looked {
		t.Error("send() looked up alertmanagers with no alerts to send")
	}
}

func TestNoDiscovery(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := newAlertDelivery(noDiscovery{}, nil, slog.New(slog.DiscardHandler), reg)

	if err := d.send(context.Background(), testAlerts()); err != nil {
		t.Errorf("send() error = %v", err)
	}
	if got := metricSeries(t, reg, "onmsgrpc_alertmanager_total"); got != 0 {
		t.Errorf("total has %d series, want none as nothing was sent", got)
	}
}

func TestStaticDiscovery(t *testing.T) {
	tests := []struct {
		name    string
		urls    []string
		want    []string
		wantErr bool
	}{
		{"none", []string{}, []string{}, false},
		{"one", []string{"http://am:9092"}, []string{"http://am:9092/api/v2/alerts"}, false},
		{"two", []string{"http://am1:9092", "http://am2:9092"}, []string{"http://am1:9092/api/v2/alerts", "http://am2:9092/api/v2/alerts"}, false},
		{"prefix", []string{"http://am:9092/prefix"}, []string{"http://am:9092/prefix/api/v2/alerts"}, false},
		{"trailing slash", []string{"http://am:9092/"}, []string{"http://am:9092/api/v2/alerts"}, false},
		{"invalid", []string{"http://am1 :9092"}, nil, true},
		{"second invalid", []string{"http://am1:9092", "http://am2 :9092"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := newStaticDiscovery(tt.urls)
			if tt.wantErr {
				if err == nil {
					t.Error("newStaticDiscovery() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("newStaticDiscovery() error = %v", err)
			}

			got, err := d.alertmanagers(context.Background())
			if err != nil {
				t.Fatalf("alertmanagers() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("alertmanagers() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeSRV is a fake SRV lookup that returns a single record whose port is
// the number of lookups made so far, so each lookup gives a different result
type fakeSRV struct {
	mu      sync.Mutex
	lookups int
	err     error

	// release, if set, is waited on before each lookup returns
	release chan struct{}
}

func (f *fakeSRV) lookup(ctx context.Context, name string) ([]*net.SRV, error) {
	if f.release != nil {
		<-f.release
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.lookups++
	if f.err != nil {
		return nil, f.err
	}

	return []*net.SRV{{Target: "am.example.com", Port: uint16(9090 + f.lookups)}}, nil
}

func (f *fakeSRV) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lookups
}

func newTestSRVDiscovery(ttl time.Duration, lookup *fakeSRV) *srvDiscovery {
	d := newSRVDiscovery("https", "_alertmanager._tcp.example.com", ttl, slog.New(slog.DiscardHandler))
	d.lookup = lookup.lookup

	return d
}

// alertmanagers calls d.alertmanagers, failing the test on error
func alertmanagers(t *testing.T, d alertmanagerDiscovery) []string {
	t.Helper()

	got, err := d.alertmanagers(context.Background())
	if err != nil {
		t.Fatalf("alertmanagers() error = %v", err)
	}

	return got
}

func TestSRVDiscoveryFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := &fakeSRV{}
		d := newTestSRVDiscovery(time.Second*30, lookup)

		want := []string{"https://am.example.com:9091/api/v2/alerts"}
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
			t.Errorf("first alertmanagers() = %v, want %v", got, want)
		}

		time.Sleep(time.Second * 29)
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
			t.Errorf("cached alertmanagers() = %v, want %v", got, want)
		}
		if got := lookup.count(); got != 1 {
			t.Errorf("lookups = %d, want 1 as the result is still fresh", got)
		}
	})
}

func TestSRVDiscoveryStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := &fakeSRV{}
		d := newTestSRVDiscovery(time.Second*30, lookup)

		alertmanagers(t, d)

		// hold the background lookup until released
		lookup.release = make(chan struct{})
		time.Sleep(time.Second * 45)

		stale := []string{"https://am.example.com:9091/api/v2/alerts"}
		for range 3 {
			if got := alertmanagers(t, d); !reflect.DeepEqual(got, stale) {
				t.Errorf("stale alertmanagers() = %v, want %v", got, stale)
			}
		}

		// only one background lookup is started, the rest waits on release
		close(lookup.release)
		synctest.Wait()

		want := []string{"https://am.example.com:9092/api/v2/alerts"}
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
			t.Errorf("alertmanagers() = %v, want refreshed %v", got, want)
		}
		if got := lookup.count(); got != 2 {
			t.Errorf("lookups = %d, want 2 as only one background lookup runs", got)
		}
	})
}

func TestSRVDiscoveryStaleRefreshFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := &fakeSRV{}
		d := newTestSRVDiscovery(time.Second*30, lookup)

		alertmanagers(t, d)

		lookup.mu.Lock()
		lookup.err = errors.New("lookup failed")
		lookup.mu.Unlock()
		time.Sleep(time.Second * 45)

		stale := []string{"https://am.example.com:9091/api/v2/alerts"}
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, stale) {
			t.Errorf("stale alertmanagers() = %v, want %v", got, stale)
		}
		synctest.Wait()

		// a failed background lookup allows another to be started
		alertmanagers(t, d)
		synctest.Wait()
		if got := lookup.count(); got != 3 {
			t.Errorf("lookups = %d, want another background lookup after a failure", got)
		}
	})
}

func TestSRVDiscoveryExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := &fakeSRV{}
		d := newTestSRVDiscovery(time.Second*30, lookup)

		alertmanagers(t, d)
		time.Sleep(time.Second * 61)

		want := []string{"https://am.example.com:9092/api/v2/alerts"}
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
			t.Errorf("expired alertmanagers() = %v, want %v looked up straight away", got, want)
		}
	})
}

func TestSRVDiscoveryExpiredLookupError(t *testing.T) {
	lookup := &fakeSRV{err: errors.New("lookup failed")}
	d := newTestSRVDiscovery(time.Second*30, lookup)

	if _, err := d.alertmanagers(context.Background()); err == nil {
		t.Error("alertmanagers() error = nil, want lookup error")
	}
}

// TestSRVDiscoveryCancelled checks a lookup made for a send stops when the
// send's context is cancelled, while a background refresh is not tied to the
// send that started it
func TestSRVDiscoveryCancelled(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ttl     time.Duration
		wait    time.Duration
		wantErr bool
	}{
		{"no cache", 0, 0, true},
		{"expired", time.Second * 30, time.Second * 61, true},
		{"stale", time.Second * 30, time.Second * 45, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				lookup := &fakeSRV{}
				d := newTestSRVDiscovery(tt.ttl, lookup)

				alertmanagers(t, d)
				time.Sleep(tt.wait)

				_, err := d.alertmanagers(cancelled)
				if tt.wantErr {
					if !errors.Is(err, context.Canceled) {
						t.Errorf("alertmanagers() error = %v, want context.Canceled", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("alertmanagers() error = %v", err)
				}

				// the background refresh still succeeds
				synctest.Wait()
				want := []string{"https://am.example.com:9092/api/v2/alerts"}
				if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
					t.Errorf("alertmanagers() = %v, want refreshed %v", got, want)
				}
			})
		})
	}
}

func TestSRVDiscoveryNoCache(t *testing.T) {
	lookup := &fakeSRV{}
	d := newTestSRVDiscovery(0, lookup)

	alertmanagers(t, d)
	want := []string{"https://am.example.com:9092/api/v2/alerts"}
	if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("alertmanagers() = %v, want %v looked up every time", got, want)
	}
}
