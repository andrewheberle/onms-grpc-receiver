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
	}))
	t.Cleanup(am.Close)

	return am
}

func testAlerts() []*models.PostableAlert {
	return []*models.PostableAlert{{
		Alert: models.Alert{Labels: models.LabelSet{"alertname": "test"}},
	}}
}

// discoveryFunc adapts a function to alertmanagerDiscovery
type discoveryFunc func() ([]string, error)

func (f discoveryFunc) alertmanagers() ([]string, error) {
	return f()
}

func TestAlertDeliverySend(t *testing.T) {
	am1 := newRecordingAlertmanager(t, http.StatusOK)
	am2 := newRecordingAlertmanager(t, http.StatusOK)

	reg := prometheus.NewRegistry()
	d := newAlertDelivery(staticDiscovery{am1.URL, am2.URL + "/prefix"}, map[string]string{"X-Scope-OrgID": "noc"}, slog.New(slog.DiscardHandler), reg)

	if err := d.send(testAlerts()); err != nil {
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

func TestAlertDeliverySendFailure(t *testing.T) {
	ok := newRecordingAlertmanager(t, http.StatusOK)
	failing := newRecordingAlertmanager(t, http.StatusInternalServerError)

	reg := prometheus.NewRegistry()
	d := newAlertDelivery(staticDiscovery{ok.URL, failing.URL}, nil, slog.New(slog.DiscardHandler), reg)

	if err := d.send(testAlerts()); err != nil {
		t.Fatalf("send() error = %v, want nil as failures are only counted", err)
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
}

func TestAlertDeliverySendLookupError(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := newAlertDelivery(discoveryFunc(func() ([]string, error) {
		return nil, errors.New("lookup failed")
	}), nil, slog.New(slog.DiscardHandler), reg)

	if err := d.send(testAlerts()); err == nil {
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

	if err := d.send(nil); err != nil {
		t.Errorf("send() error = %v", err)
	}
	if looked {
		t.Error("send() looked up alertmanagers with no alerts to send")
	}
}

func TestNoDiscovery(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := newAlertDelivery(noDiscovery{}, nil, slog.New(slog.DiscardHandler), reg)

	if err := d.send(testAlerts()); err != nil {
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
		{"one", []string{"http://am:9092"}, []string{"http://am:9092"}, false},
		{"two", []string{"http://am1:9092", "http://am2:9092"}, []string{"http://am1:9092", "http://am2:9092"}, false},
		{"prefix", []string{"http://am:9092/prefix"}, []string{"http://am:9092/prefix"}, false},
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

			got, err := d.alertmanagers()
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

	got, err := d.alertmanagers()
	if err != nil {
		t.Fatalf("alertmanagers() error = %v", err)
	}

	return got
}

func TestSRVDiscoveryFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := &fakeSRV{}
		d := newTestSRVDiscovery(time.Second*30, lookup)

		want := []string{"https://am.example.com:9091"}
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

		stale := []string{"https://am.example.com:9091"}
		for range 3 {
			if got := alertmanagers(t, d); !reflect.DeepEqual(got, stale) {
				t.Errorf("stale alertmanagers() = %v, want %v", got, stale)
			}
		}

		// only one background lookup is started, the rest waits on release
		close(lookup.release)
		synctest.Wait()

		want := []string{"https://am.example.com:9092"}
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

		stale := []string{"https://am.example.com:9091"}
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

		want := []string{"https://am.example.com:9092"}
		if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
			t.Errorf("expired alertmanagers() = %v, want %v looked up straight away", got, want)
		}
	})
}

func TestSRVDiscoveryExpiredLookupError(t *testing.T) {
	lookup := &fakeSRV{err: errors.New("lookup failed")}
	d := newTestSRVDiscovery(time.Second*30, lookup)

	if _, err := d.alertmanagers(); err == nil {
		t.Error("alertmanagers() error = nil, want lookup error")
	}
}

func TestSRVDiscoveryNoCache(t *testing.T) {
	lookup := &fakeSRV{}
	d := newTestSRVDiscovery(0, lookup)

	alertmanagers(t, d)
	want := []string{"https://am.example.com:9092"}
	if got := alertmanagers(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("alertmanagers() = %v, want %v looked up every time", got, want)
	}
}
