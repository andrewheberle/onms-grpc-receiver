package cmd

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSpogRejectsBuiltinNames(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
	}{
		{"label flag", []string{"--alert.labels", "severity=critical"}, nil, `"severity" clashes with a built-in label`},
		{"annotation flag", []string{"--alert.annotations", "runbook_url=https://wiki/runbooks/opennms"}, nil, `"runbook_url" clashes with a built-in annotation`},
		{"label env var", nil, map[string]string{"ONMS_GRPC_ALERT_LABELS": "env=prod,alertname=x"}, `"alertname" clashes with a built-in label`},
		{"annotation env var", nil, map[string]string{"ONMS_GRPC_ALERT_ANNOTATIONS": "summary=x"}, `"summary" clashes with a built-in annotation`},
		{"runbook url template flag", []string{"--alert.runbook-url", "https://runbooks.example.com/{{ .Lables.alertname }}"}, nil, "invalid runbook URL template"},
		{"runbook url template env var", nil, map[string]string{"ONMS_GRPC_ALERT_RUNBOOK_URL": "https://runbooks.example.com/{{ .Labels.alertname"}, "invalid runbook URL template"},
		{"severity map flag", []string{"--alert.severity", "cleared=resolved"}, nil, `invalid severity map: "cleared" is not a firing severity`},
		{"severity map env var", nil, map[string]string{"ONMS_GRPC_ALERT_SEVERITY": "major=P2,crit=P1"}, `invalid severity map: "crit" is not an OpenNMS severity`},
		{"negative resolve hold flag", []string{"--resolve.hold", "-1m"}, nil, "invalid resolve hold -1m0s"},
		{"negative resolve hold env var", nil, map[string]string{"ONMS_GRPC_RESOLVE_HOLD": "-5m"}, "invalid resolve hold -5m0s"},
		{"metrics path flag", []string{"--metrics.path", "metrics"}, nil, `invalid --metrics.path "metrics": must start with "/"`},
		{"metrics path env var", nil, map[string]string{"ONMS_GRPC_METRICS_PATH": "/-/healthy"}, `invalid --metrics.path "/-/healthy": clashes with a health check`},
		{"metrics path ready", []string{"--metrics.path", "/-/ready"}, nil, `invalid --metrics.path "/-/ready": clashes with a health check`},
		{"metrics path with wildcard", []string{"--metrics.path", "/{metrics}"}, nil, `invalid --metrics.path "/{metrics}": must not contain "{" or "}"`},
		{"metrics path with space", []string{"--metrics.path", "GET /metrics"}, nil, `invalid --metrics.path "GET /metrics": must start with "/"`},
		{"max recv size flag", []string{"--grpc.max-recv-size", "0"}, nil, "invalid --grpc.max-recv-size 0: must be greater than zero"},
		{"max recv size env var", nil, map[string]string{"ONMS_GRPC_GRPC_MAX_RECV_SIZE": "-1"}, "invalid --grpc.max-recv-size -1: must be greater than zero"},
		{"headers flag", []string{"--headers", "Host=am"}, nil, `invalid header "Host": Host cannot be set`},
		{"headers env var", nil, map[string]string{"ONMS_GRPC_HEADERS": "X Scope=noc"}, `invalid header "X Scope": not a valid header name`},
		{"srv ttl flag", []string{"--alertmanager.srv", "_alertmanager._tcp.example.com", "--srv.ttl", "-30s"}, nil, "invalid SRV cache TTL -30s: must not be negative"},
		{"srv ttl env var", nil, map[string]string{"ONMS_GRPC_SRV_TTL": "-1m"}, "invalid SRV cache TTL -1m0s: must not be negative"},
		{"alertmanager url flag", []string{"--alertmanager.url", "localhost:9093"}, nil, `invalid alertmanager URL "localhost:9093": must be http or https`},
		{"alertmanager scheme flag", []string{"--alertmanager.srv", "_alertmanager._tcp.example.com", "--alertmanager.scheme", "htps"}, nil, `invalid alertmanager scheme "htps"`},
		{"map url env var", nil, map[string]string{"ONMS_GRPC_MAP_URL": "6f8a7c1e-uuid=http://horizon:8980/opennms/#alarms"}, "must not have a fragment"},
		{"map url credentials flag", []string{"--map.url", "6f8a7c1e-uuid=http://admin:secret@horizon:8980/opennms/"}, nil, `invalid URL for instance "6f8a7c1e-uuid" in URL map "http://horizon:8980/opennms/": must not have credentials`},
		{"map url credentials env var", nil, map[string]string{"ONMS_GRPC_MAP_URL": "6f8a7c1e-uuid=https://admin@horizon:8980/opennms/"}, "must not have credentials"},
		{"every problem at once", []string{"--metrics.path", "metrics", "--resolve.hold", "-1m"}, nil, `invalid --metrics.path "metrics": must start with "/"` + "\ninvalid resolve hold -1m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			// the receiver runs until stopped if startup does not fail
			done := make(chan error, 1)
			go func() {
				done <- Execute(context.Background(), append([]string{"spog", "--silent", "--address", "127.0.0.1:0"}, tt.args...))
			}()

			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Execute() error = %v, want containing %q", err, tt.wantErr)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Execute() did not fail on startup")
			}
		})
	}
}

func TestSpogStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"spog", "--silent", "--address", "127.0.0.1:0"})
	}()

	// give the receiver time to start
	select {
	case err := <-done:
		t.Fatalf("Execute() returned %v before being cancelled", err)
	case <-time.After(time.Millisecond * 200):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Execute() error = %v, want nil after being cancelled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute() did not stop after being cancelled")
	}
}

// freeAddress returns a local address with a port that is free, for a
// listener the test cannot pass port 0 to and then ask for its address
func freeAddress(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	return addr
}

// TestSpogHealthChecks checks the liveness and readiness checks are served
// with metrics
func TestSpogHealthChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := freeAddress(t)
	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"spog", "--silent", "--address", "127.0.0.1:0", "--metrics.address", addr})
	}()

	tests := []struct {
		path     string
		wantBody string
	}{
		{"/-/healthy", "Healthy"},
		{"/-/ready", "Ready"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// the receiver may still be starting
			var code int
			var body string
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				resp, err := http.Get("http://" + addr + tt.path)
				if err == nil {
					b, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					code, body = resp.StatusCode, string(b)
					if code == http.StatusOK {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}

			if code != http.StatusOK || body != tt.wantBody {
				t.Errorf("GET %s = %d %q, want 200 %q", tt.path, code, body, tt.wantBody)
			}
		})
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Execute() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute() did not stop after being cancelled")
	}
}
