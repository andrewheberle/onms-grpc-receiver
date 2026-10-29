package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/models"
)

func TestPostableAlertCompatibility(t *testing.T) {
	now := time.Date(2026, 10, 29, 1, 2, 3, 456789000, time.UTC)

	tests := []struct {
		name  string
		alert postableAlert
	}{
		{"labels only", postableAlert{
			Labels: map[string]string{"alertname": "test"},
		}},
		{"all fields", postableAlert{
			Labels:       map[string]string{"alertname": "test", "severity": "critical"},
			Annotations:  map[string]string{"summary": "x"},
			GeneratorURL: "https://opennms.example.com/opennms/alarm/detail.htm?id=1",
			StartsAt:     now,
			EndsAt:       now.Add(time.Minute * 5),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal([]*postableAlert{&tt.alert})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var got models.PostableAlerts
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if err := got.Validate(strfmt.Default); err != nil {
				t.Fatalf("Validate() error = %v, payload %s", err, payload)
			}
			if len(got) != 1 {
				t.Fatalf("got %d alerts, want 1", len(got))
			}

			a := got[0]
			if len(a.Labels) != len(tt.alert.Labels) {
				t.Errorf("Labels = %v, want %v", a.Labels, tt.alert.Labels)
			}
			for k, v := range tt.alert.Labels {
				if a.Labels[k] != v {
					t.Errorf("Labels[%q] = %q, want %q", k, a.Labels[k], v)
				}
			}
			if len(a.Annotations) != len(tt.alert.Annotations) {
				t.Errorf("Annotations = %v, want %v", a.Annotations, tt.alert.Annotations)
			}
			for k, v := range tt.alert.Annotations {
				if a.Annotations[k] != v {
					t.Errorf("Annotations[%q] = %q, want %q", k, a.Annotations[k], v)
				}
			}
			if string(a.GeneratorURL) != tt.alert.GeneratorURL {
				t.Errorf("GeneratorURL = %q, want %q", a.GeneratorURL, tt.alert.GeneratorURL)
			}
			if startsAt := time.Time(a.StartsAt); !startsAt.Equal(tt.alert.StartsAt) {
				t.Errorf("StartsAt = %v, want %v", startsAt, tt.alert.StartsAt)
			}
			if endsAt := time.Time(a.EndsAt); !endsAt.Equal(tt.alert.EndsAt) {
				t.Errorf("EndsAt = %v, want %v", endsAt, tt.alert.EndsAt)
			}
		})
	}
}
