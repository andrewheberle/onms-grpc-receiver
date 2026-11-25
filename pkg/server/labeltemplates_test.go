package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
)

func TestNewAlertTranslatorLabelTemplates(t *testing.T) {
	tests := []struct {
		name      string
		templates map[string]string
		wantErr   string
	}{
		{"none", nil, ""},
		{"valid", map[string]string{"device_class": `{{ if hasPrefix .Alarm.NodeLabel "sw-" }}switch{{ end }}`, "node_id": "{{ .Alarm.NodeID }}"}, ""},
		{"uses an extra label", map[string]string{"team": "{{ .Labels.env }}-noc"}, ""},
		{"builtin", map[string]string{"severity": "x"}, `invalid label templates: "severity" clashes with a built-in label`},
		{"extra label", map[string]string{"env": "x"}, `invalid label templates: "env" clashes with an extra label`},
		{"invalid name", map[string]string{"device-class": "x"}, `invalid label templates: "device-class" is not a valid name`},
		{"reserved", map[string]string{"__class": "x"}, `invalid label templates: "__class" is reserved as it starts with "__"`},
		{"empty template", map[string]string{"class": ""}, `invalid label templates: "class" has an empty template`},
		{"parse error", map[string]string{"class": "{{ .Alarm.NodeLabel"}, `invalid label templates: "class": template: class:1: unclosed action`},
		{"unknown field", map[string]string{"class": "{{ .Alarm.Nodelabel }}"}, `invalid label templates: "class": template: class:1:9: executing "class" at <.Alarm.Nodelabel>: can't evaluate field Nodelabel`},
		{"unknown function", map[string]string{"class": "{{ lower .Alarm.NodeLabel }}"}, `invalid label templates: "class": template: class:1: function "lower" not defined`},
		{"invalid regular expression", map[string]string{"class": `{{ reReplaceAll "(" "" .Alarm.NodeLabel }}`}, `invalid label templates: "class": template: class:1:3: executing "class" at <reReplaceAll "(" "" .Alarm.NodeLabel>: error calling reReplaceAll: error parsing regexp: missing closing ): ` + "`(`"},
		{"multiple", map[string]string{"severity": "x", "class": ""}, `invalid label templates: "class" has an empty template; "severity" clashes with a built-in label`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newAlertTranslator(translationConfig{
				extraLabels:    map[string]string{"env": "prod"},
				labelTemplates: tt.templates,
				resolveTimeout: time.Minute,
			}, slog.New(slog.DiscardHandler))
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("newAlertTranslator() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("newAlertTranslator() error = %v, want prefix %q", err, tt.wantErr)
			}
		})
	}
}

// templateTestAlarm returns an alarm with every detail a label template can
// use set, last updated at lastUpdate
func templateTestAlarm(severity pb.Severity, lastUpdate time.Time) *pb.Alarm {
	return pb.Alarm_builder{
		Id:  25,
		Uei: "uei.opennms.org/nodes/nodeDown",
		NodeCriteria: pb.NodeCriteria_builder{
			Id:            12,
			ForeignSource: "switches",
			ForeignId:     "1234",
			NodeLabel:     " SW-Core1.example.com ",
			Location:      "Perth",
		}.Build(),
		IpAddress:             "10.0.0.1",
		ServiceName:           "ICMP",
		ReductionKey:          "uei.opennms.org/nodes/nodeDown::12",
		ClearKey:              "uei.opennms.org/nodes/nodeUp::12",
		IfIndex:               3,
		ManagedObjectType:     "node",
		ManagedObjectInstance: "12",
		Severity:              uint32(severity),
		FirstEventTime:        uint64(lastUpdate.Add(-time.Hour).UnixMilli()),
		LastEventTime:         uint64(lastUpdate.UnixMilli()),
		LastUpdateTime:        uint64(lastUpdate.UnixMilli()),
	}.Build()
}

// TestLabelTemplateData checks the details of an alarm and the functions a
// label template can use
func TestLabelTemplateData(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{"id", "{{ .Alarm.ID }}", "25"},
		{"uei", "{{ .Alarm.UEI }}", "uei.opennms.org/nodes/nodeDown"},
		{"node id", "{{ .Alarm.NodeID }}", "12"},
		{"node label", "{{ .Alarm.NodeLabel }}", " SW-Core1.example.com "},
		{"foreign source", "{{ .Alarm.ForeignSource }}", "switches"},
		{"foreign id", "{{ .Alarm.ForeignID }}", "1234"},
		{"location", "{{ .Alarm.Location }}", "Perth"},
		{"ip address", "{{ .Alarm.IPAddress }}", "10.0.0.1"},
		{"service name", "{{ .Alarm.ServiceName }}", "ICMP"},
		{"reduction key", "{{ .Alarm.ReductionKey }}", "uei.opennms.org/nodes/nodeDown::12"},
		{"clear key", "{{ .Alarm.ClearKey }}", "uei.opennms.org/nodes/nodeUp::12"},
		{"if index", "{{ .Alarm.IfIndex }}", "3"},
		{"managed object type", "{{ .Alarm.ManagedObjectType }}", "node"},
		{"managed object instance", "{{ .Alarm.ManagedObjectInstance }}", "12"},
		{"built-in label", "{{ .Labels.node_name }}", " SW-Core1.example.com "},
		{"severity label", "{{ .Labels.severity }}", "major"},
		{"extra label", "{{ .Labels.env }}", "prod"},
		{"missing label", "{{ .Labels.missing }}x", "x"},
		{"toLower", "{{ toLower .Alarm.ForeignSource }}{{ toLower .Alarm.NodeLabel }}", "switches sw-core1.example.com "},
		{"toUpper", "{{ toUpper .Alarm.Location }}", "PERTH"},
		{"trimSpace", "{{ trimSpace .Alarm.NodeLabel }}", "SW-Core1.example.com"},
		{"hasPrefix", `{{ hasPrefix (trimSpace .Alarm.NodeLabel) "SW-" }}`, "true"},
		{"hasSuffix", `{{ hasSuffix (trimSpace .Alarm.NodeLabel) ".example.com" }}`, "true"},
		{"contains", `{{ contains .Alarm.NodeLabel "Core" }}`, "true"},
		{"match", `{{ match "^ ?SW-" .Alarm.NodeLabel }}`, "true"},
		{"reReplaceAll", `{{ reReplaceAll "^ *([A-Z]+)-.*$" "$1" .Alarm.NodeLabel }}`, "SW"},
		{"pathEscape", "{{ pathEscape .Alarm.ReductionKey }}", "uei.opennms.org%2Fnodes%2FnodeDown::12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTranslator(t, translationConfig{
				extraLabels:    map[string]string{"env": "prod"},
				labelTemplates: map[string]string{"x": tt.template},
			})

			got := firstAlarmAlert(tr, instanceAlarm{alarm: templateTestAlarm(pb.Severity_MAJOR, now), now: now, instanceName: "OpenNMS"})

			if got.Labels["x"] != tt.want {
				t.Errorf("label = %q, want %q", got.Labels["x"], tt.want)
			}
		})
	}
}

// TestAlertTranslatorAlarmLabelTemplates checks label templates add their
// labels to the firing and resolved alerts of an alarm, leave out empty
// values, and are seen by the runbook URL template
func TestAlertTranslatorAlarmLabelTemplates(t *testing.T) {
	now := time.Now()

	tr := newTestTranslator(t, translationConfig{
		labelTemplates: map[string]string{
			"device_class": `{{ if hasPrefix (trimSpace .Alarm.NodeLabel) "SW-" }}switch{{ end }}`,
			"page":         `{{ if eq .Labels.severity "critical" }}yes{{ end }}`,
			"vm":           `{{ if eq .Alarm.ForeignSource "vmware" }}yes{{ end }}`,
		},
		runbookURLTemplate: "https://runbooks.example.com/{{ .Labels.device_class }}",
	})

	// fired with an unknown Firing severity, so every other severity is
	// resolved too
	alerts, _, _ := tr.alarm(instanceAlarm{alarm: templateTestAlarm(pb.Severity_MAJOR, now), now: now, instanceName: "OpenNMS"}, firingSeverity{})
	if len(alerts) != 5 {
		t.Fatalf("sent %d alerts, want 5", len(alerts))
	}

	for _, a := range alerts {
		severity := a.Labels["severity"]
		if got := a.Labels["device_class"]; got != "switch" {
			t.Errorf("%s alert device_class = %q, want switch", severity, got)
		}
		if _, ok := a.Labels["vm"]; ok {
			t.Errorf("%s alert has vm label, want none for an empty value", severity)
		}
		if _, got := a.Labels["page"]; got != (severity == "critical") {
			t.Errorf("%s alert has page label = %v, want %v", severity, got, severity == "critical")
		}
		if got := a.Annotations["runbook_url"]; got != "https://runbooks.example.com/switch" {
			t.Errorf("%s alert runbook_url = %q, want https://runbooks.example.com/switch", severity, got)
		}
	}
}

// TestAlertTranslatorLabelTemplateFailure checks a template that fails for an
// alarm adds no label, is logged, and does not stop the alert being sent
func TestAlertTranslatorLabelTemplateFailure(t *testing.T) {
	now := time.Now()

	var logs bytes.Buffer
	tr, err := newAlertTranslator(translationConfig{
		labelTemplates: map[string]string{
			"broken":       `{{ reReplaceAll .Alarm.ForeignID "" "x" }}`,
			"device_class": "switch",
		},
		resolveTimeout: time.Minute,
	}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("newAlertTranslator() error = %v", err)
	}

	// a foreign ID that is not a valid regular expression
	alarm := templateTestAlarm(pb.Severity_MAJOR, now.Add(-time.Hour))
	alarm.GetNodeCriteria().SetForeignId("(")

	got := firstAlarmAlert(tr, instanceAlarm{alarm: alarm, now: now, instanceName: "OpenNMS"})

	if _, ok := got.Labels["broken"]; ok {
		t.Errorf("labels = %v, want no broken label", got.Labels)
	}
	if got.Labels["device_class"] != "switch" {
		t.Errorf("labels = %v, want device_class switch", got.Labels)
	}
	if want := `level=WARN msg="problem executing label template" label=broken alarm_id=25`; !strings.Contains(logs.String(), want) {
		t.Errorf("logs = %q, want containing %q", logs.String(), want)
	}
}

// TestAlertTranslatorHeartbeatLabelTemplates checks label templates are not
// applied to the heartbeat alert, which has no alarm
func TestAlertTranslatorHeartbeatLabelTemplates(t *testing.T) {
	tr := newTestTranslator(t, translationConfig{
		labelTemplates: map[string]string{"device_class": "switch"},
	})

	got := tr.heartbeat("OpenNMS", time.Now())

	if _, ok := got.Labels["device_class"]; ok {
		t.Errorf("heartbeat labels = %v, want no device_class", got.Labels)
	}
}
