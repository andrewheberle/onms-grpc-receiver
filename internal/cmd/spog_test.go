package cmd

import (
	"context"
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
