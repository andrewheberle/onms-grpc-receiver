package cmd

import (
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			// the receiver runs until stopped if startup does not fail
			done := make(chan error, 1)
			go func() {
				done <- Execute(append([]string{"spog", "--silent", "--address", "127.0.0.1:0"}, tt.args...))
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
