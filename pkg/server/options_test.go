package server

import (
	"reflect"
	"testing"
)

func TestWithAlertmanagerUrl(t *testing.T) {

	tests := []struct {
		name       string
		list       []string
		want       []string
		wantOptErr bool
		wantErr    bool
	}{
		{"none", []string{}, []string{}, false, false},
		{"one", []string{"http://am:9092"}, []string{"http://am:9092/api/v2/alerts"}, false, false},
		{"two", []string{"http://am1:9092", "http://am2:9092"}, []string{"http://am1:9092/api/v2/alerts", "http://am2:9092/api/v2/alerts"}, false, false},
		{"invalid", []string{"http://am1 :9092"}, []string{}, true, false},
		{"second invalid", []string{"http://am1:9092", "http://am2 :9092"}, []string{}, true, false},
		{"prefix", []string{"http://am:9092/prefix"}, []string{"http://am:9092/prefix/api/v2/alerts"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ServiceSyncServer{}
			o := WithAlertmanagerUrl(tt.list)
			gotOptErr := o(srv)
			if tt.wantOptErr {
				if gotOptErr == nil {
					t.Errorf("WithAlertmanagerUrl() = %v, wantOptErr %v", gotOptErr, tt.wantOptErr)
				}
				return
			}

			got, gotErr := srv.alertmanagers()
			if tt.wantErr && gotErr == nil {
				t.Errorf("WithAlertmanagerUrl() = %v, wantErr %v", gotErr, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("WithAlertmanagerUrl() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithAlertLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		wantErr string
	}{
		{"none", map[string]string{}, ""},
		{"valid", map[string]string{"env": "prod", "team_name": "noc", "_private": "x"}, ""},
		{"builtin", map[string]string{"severity": "critical"}, `invalid alert labels: "severity" clashes with a built-in label`},
		{"builtin only sometimes set", map[string]string{"service": "x"}, `invalid alert labels: "service" clashes with a built-in label`},
		{"builtin annotation is allowed", map[string]string{"summary": "x"}, ""},
		{"invalid name", map[string]string{"team-name": "noc"}, `invalid alert labels: "team-name" is not a valid name`},
		{"starts with digit", map[string]string{"1env": "prod"}, `invalid alert labels: "1env" is not a valid name`},
		{"reserved", map[string]string{"__env": "prod"}, `invalid alert labels: "__env" is reserved as it starts with "__"`},
		{"empty value", map[string]string{"env": ""}, `invalid alert labels: "env" has an empty value`},
		{"multiple", map[string]string{"severity": "x", "alertname": "y", "env": ""}, `invalid alert labels: "alertname" clashes with a built-in label; "env" has an empty value; "severity" clashes with a built-in label`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ServiceSyncServer{}
			err := WithAlertLabels(tt.labels)(srv)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("WithAlertLabels() error = %v, want nil", err)
				}
				if !reflect.DeepEqual(srv.alertLabels, tt.labels) {
					t.Errorf("WithAlertLabels() alertLabels = %v, want %v", srv.alertLabels, tt.labels)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("WithAlertLabels() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestWithAlertAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     string
	}{
		{"none", map[string]string{}, ""},
		{"valid", map[string]string{"dashboard": "https://grafana/d/onms", "note": "Call the NOC, then escalate"}, ""},
		{"runbook_url", map[string]string{"runbook_url": "https://wiki/runbooks/opennms"}, `invalid alert annotations: "runbook_url" clashes with a built-in annotation`},
		{"summary", map[string]string{"summary": "x"}, `invalid alert annotations: "summary" clashes with a built-in annotation`},
		{"builtin label is allowed", map[string]string{"severity": "x"}, ""},
		{"invalid name", map[string]string{"my.note": "x"}, `invalid alert annotations: "my.note" is not a valid name`},
		{"reserved", map[string]string{"__note": "x"}, `invalid alert annotations: "__note" is reserved as it starts with "__"`},
		{"empty value", map[string]string{"note": ""}, `invalid alert annotations: "note" has an empty value`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ServiceSyncServer{}
			err := WithAlertAnnotations(tt.annotations)(srv)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("WithAlertAnnotations() error = %v, want nil", err)
				}
				if !reflect.DeepEqual(srv.alertAnnotations, tt.annotations) {
					t.Errorf("WithAlertAnnotations() alertAnnotations = %v, want %v", srv.alertAnnotations, tt.annotations)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("WithAlertAnnotations() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
