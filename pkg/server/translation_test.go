package server

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
)

func TestAlertTranslatorAlarmSkips(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) uint64 {
		return uint64(now.Add(d).UnixMilli())
	}

	tests := []struct {
		name  string
		alarm *pb.Alarm
		want  bool
	}{
		{"new firing alarm", pb.Alarm_builder{
			Id:             1,
			Severity:       uint32(pb.Severity_MAJOR),
			FirstEventTime: ms(-time.Minute),
			LastEventTime:  ms(-time.Minute),
			LastUpdateTime: ms(-time.Minute),
		}.Build(), true},
		{"long running firing alarm from snapshot", pb.Alarm_builder{
			Id:             2,
			Severity:       uint32(pb.Severity_CRITICAL),
			FirstEventTime: ms(-time.Hour * 24),
			LastEventTime:  ms(-time.Hour),
			LastUpdateTime: ms(-time.Hour),
		}.Build(), true},
		{"recently cleared alarm with old events", pb.Alarm_builder{
			Id:             3,
			Severity:       uint32(pb.Severity_CLEARED),
			FirstEventTime: ms(-time.Hour * 2),
			LastEventTime:  ms(-time.Hour),
			LastUpdateTime: ms(-time.Second * 10),
		}.Build(), true},
		{"old cleared alarm from snapshot", pb.Alarm_builder{
			Id:             4,
			Severity:       uint32(pb.Severity_CLEARED),
			FirstEventTime: ms(-time.Hour * 2),
			LastEventTime:  ms(-time.Hour),
			LastUpdateTime: ms(-time.Hour),
		}.Build(), false},
		{"normal alarm", pb.Alarm_builder{
			Id:             5,
			Severity:       uint32(pb.Severity_NORMAL),
			FirstEventTime: ms(-time.Minute),
			LastEventTime:  ms(-time.Minute),
			LastUpdateTime: ms(-time.Minute),
		}.Build(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &alertTranslator{}

			got, err := tr.alarm(instanceAlarm{alarm: tt.alarm, now: now, instanceID: "test"})
			if err != nil {
				t.Fatalf("alarm() error = %v", err)
			}

			if sent := got != nil; sent != tt.want {
				t.Errorf("alarm() sent = %v, want %v", sent, tt.want)
			}
		})
	}
}

func TestAlertTranslatorAlarmEndsAt(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) uint64 {
		return uint64(now.Add(d).UnixMilli())
	}

	// received a while before being translated, as happens when batched
	now = now.Add(-time.Second * 20)

	tr := &alertTranslator{resolveTimeout: time.Minute * 3}

	firing, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
		Id:             1,
		Severity:       uint32(pb.Severity_MAJOR),
		FirstEventTime: ms(-time.Hour),
		LastEventTime:  ms(-time.Hour),
		LastUpdateTime: ms(-time.Hour),
	}.Build(), now: now, instanceID: "test"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}
	if endsAt, want := time.Time(firing.EndsAt), now.Add(time.Minute*3); !endsAt.Equal(want) {
		t.Errorf("firing alert EndsAt = %v, want %v", endsAt, want)
	}

	cleared, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
		Id:             2,
		Severity:       uint32(pb.Severity_CLEARED),
		FirstEventTime: ms(-time.Hour),
		LastEventTime:  ms(-time.Hour),
		LastUpdateTime: ms(-time.Second),
	}.Build(), now: now, instanceID: "test"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}
	if endsAt, want := time.Time(cleared.EndsAt), time.UnixMilli(int64(ms(-time.Second))); !endsAt.Equal(want) {
		t.Errorf("cleared alert EndsAt = %v, want last update time %v", endsAt, want)
	}
}

func TestAlertTranslatorAlarmLabelsAndAnnotations(t *testing.T) {
	now := time.Now()

	tr := &alertTranslator{}

	got, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
		Id:  25,
		Uei: "uei.opennms.org/nodes/nodeLostService",
		NodeCriteria: pb.NodeCriteria_builder{
			Id:            12,
			ForeignSource: "customer-a",
			ForeignId:     "1234",
			NodeLabel:     "router1",
			Location:      "Perth",
		}.Build(),
		IpAddress:            "10.0.0.1",
		ServiceName:          "ICMP",
		ReductionKey:         "uei.opennms.org/nodes/nodeLostService::12:10.0.0.1:ICMP",
		ClearKey:             "uei.opennms.org/nodes/nodeRegainedService::12:10.0.0.1:ICMP",
		Severity:             uint32(pb.Severity_MAJOR),
		FirstEventTime:       uint64(now.UnixMilli()),
		LastEventTime:        uint64(now.UnixMilli()),
		LogMessage:           "ICMP outage identified on interface 10.0.0.1.",
		Description:          "<p>A ICMP outage was identified on interface\n      10.0.0.1.</p> <p>A new Outage record has been created &amp; service level\n      availability calculations will be impacted.</p>",
		OperatorInstructions: "<p>Check the router.</p>",
	}.Build(), now: now, instanceID: "6f8a7c1e-uuid", instanceName: "OpenNMS"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}

	wantLabels := map[string]string{
		"alertname":      "uei.opennms.org/nodes/nodeLostService",
		"alarm_id":       "25",
		"node_name":      "router1",
		"foreign_source": "customer-a",
		"instance_name":  "OpenNMS",
		"severity":       "major",
		"service":        "ICMP",
		"ip_address":     "10.0.0.1",
		"site":           "Perth",
	}
	if !reflect.DeepEqual(map[string]string(got.Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got.Labels, wantLabels)
	}

	wantAnnotations := map[string]string{
		"summary":               "ICMP outage identified on interface 10.0.0.1.",
		"description":           "A ICMP outage was identified on interface 10.0.0.1. A new Outage record has been created & service level availability calculations will be impacted.",
		"operator_instructions": "Check the router.",
	}
	if !reflect.DeepEqual(map[string]string(got.Annotations), wantAnnotations) {
		t.Errorf("annotations = %v, want %v", got.Annotations, wantAnnotations)
	}
}

func TestAlertTranslatorAlarmOmitsEmptyAnnotations(t *testing.T) {
	now := time.Now()

	tr := &alertTranslator{}

	got, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
		Id:             1,
		Uei:            "uei.opennms.org/nodes/nodeDown",
		Severity:       uint32(pb.Severity_MAJOR),
		FirstEventTime: uint64(now.UnixMilli()),
		LastEventTime:  uint64(now.UnixMilli()),
		Description:    "<p> </p>",
	}.Build(), now: now, instanceName: "OpenNMS"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}

	if len(got.Annotations) != 0 {
		t.Errorf("annotations = %v, want none", got.Annotations)
	}
	if _, ok := got.Labels["foreign_source"]; ok {
		t.Errorf("labels = %v, want no foreign_source", got.Labels)
	}
}

func TestAlertTranslatorAlarmGeneratorURL(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		instanceID string
		want       string
	}{
		{"mapped instance", "6f8a7c1e-uuid", "https://horizon.example.com/opennms/alarm/detail.htm?id=25"},
		{"unmapped instance", "other-uuid", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &alertTranslator{
				urlMap: map[string]string{"6f8a7c1e-uuid": "https://horizon.example.com/opennms"},
			}

			got, err := tr.alarm(instanceAlarm{alarm: fullAlarm(now), now: now, instanceID: tt.instanceID})
			if err != nil {
				t.Fatalf("alarm() error = %v", err)
			}
			if string(got.GeneratorURL) != tt.want {
				t.Errorf("GeneratorURL = %q, want %q", got.GeneratorURL, tt.want)
			}
		})
	}
}

func TestOperatorInstructions(t *testing.T) {
	tests := []struct {
		name         string
		instructions string
		wantRunbook  string
		wantText     string
	}{
		{"empty", "", "", ""},
		{"empty html", "<p> </p>", "", ""},
		{"text only", "<p>Check the router.</p>", "", "Check the router."},
		{"plain url", "https://wiki.example.com/runbooks/node-down", "https://wiki.example.com/runbooks/node-down", ""},
		{"plain url with whitespace", "\n  https://wiki.example.com/runbooks/node-down\n", "https://wiki.example.com/runbooks/node-down", ""},
		{"url in paragraph", "<p>https://wiki.example.com/runbooks/node-down</p>", "https://wiki.example.com/runbooks/node-down", ""},
		{"link with url as text", `<a href="https://wiki.example.com/runbooks/node-down">https://wiki.example.com/runbooks/node-down</a>`, "https://wiki.example.com/runbooks/node-down", ""},
		{"url in text", "Restart the service, see https://wiki.example.com/runbooks/svc.", "https://wiki.example.com/runbooks/svc", "Restart the service, see https://wiki.example.com/runbooks/svc."},
		{"html link", `<p>Check <a href="https://wiki.example.com/runbooks/svc?a=1&amp;b=2">the runbook</a> first</p>`, "https://wiki.example.com/runbooks/svc?a=1&b=2", "Check the runbook first"},
		{"first url wins", "See http://one.example.com/a or http://two.example.com/b", "http://one.example.com/a", "See http://one.example.com/a or http://two.example.com/b"},
		{"url in brackets", "Check the runbook (https://wiki.example.com/runbooks/svc)", "https://wiki.example.com/runbooks/svc", "Check the runbook (https://wiki.example.com/runbooks/svc)"},
		{"non http link ignored", `<a href="mailto:noc@example.com">Email the NOC</a>`, "", "Email the NOC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRunbook, gotText := operatorInstructions(tt.instructions)
			if gotRunbook != tt.wantRunbook {
				t.Errorf("operatorInstructions() runbook = %q, want %q", gotRunbook, tt.wantRunbook)
			}
			if gotText != tt.wantText {
				t.Errorf("operatorInstructions() text = %q, want %q", gotText, tt.wantText)
			}
		})
	}
}

func TestAlertTranslatorAlarmRunbookURL(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		instructions string
		want         map[string]string
	}{
		{"url only", "https://wiki.example.com/runbooks/node-down", map[string]string{
			"runbook_url": "https://wiki.example.com/runbooks/node-down",
		}},
		{"url and text", `<p>Check <a href="https://wiki.example.com/runbooks/node-down">the runbook</a></p>`, map[string]string{
			"runbook_url":           "https://wiki.example.com/runbooks/node-down",
			"operator_instructions": "Check the runbook",
		}},
		{"text only", "<p>Check the router.</p>", map[string]string{
			"operator_instructions": "Check the router.",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &alertTranslator{}

			got, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
				Id:                   1,
				Uei:                  "uei.opennms.org/nodes/nodeDown",
				Severity:             uint32(pb.Severity_MAJOR),
				FirstEventTime:       uint64(now.UnixMilli()),
				LastEventTime:        uint64(now.UnixMilli()),
				OperatorInstructions: tt.instructions,
			}.Build(), now: now, instanceName: "OpenNMS"})
			if err != nil {
				t.Fatalf("alarm() error = %v", err)
			}

			if !reflect.DeepEqual(map[string]string(got.Annotations), tt.want) {
				t.Errorf("annotations = %v, want %v", got.Annotations, tt.want)
			}
			if _, ok := got.Labels["runbook_url"]; ok {
				t.Errorf("labels = %v, want no runbook_url label", got.Labels)
			}
		})
	}
}

func TestAlertTranslatorRunbookURLTemplate(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		template     string
		instructions string
		want         string
	}{
		{"no template", "", "", ""},
		{"template", "https://runbooks.example.com/{{ .Labels.alertname }}", "", "https://runbooks.example.com/uei.opennms.org/nodes/nodeDown"},
		{"instructions url wins", "https://runbooks.example.com/{{ .Labels.alertname }}", "https://wiki.example.com/runbooks/node-down", "https://wiki.example.com/runbooks/node-down"},
		{"instructions without url", "https://runbooks.example.com/{{ .Labels.alertname }}", "Check the router.", "https://runbooks.example.com/uei.opennms.org/nodes/nodeDown"},
		{"path escaped", "https://runbooks.example.com/{{ pathEscape .Labels.alertname }}", "", "https://runbooks.example.com/uei.opennms.org%2Fnodes%2FnodeDown"},
		{"extra label", "https://runbooks.example.com/{{ .Labels.env }}/{{ .Labels.node_name }}", "", "https://runbooks.example.com/prod/router1"},
		{"missing label", "https://runbooks.example.com/{{ .Labels.missing }}", "", "https://runbooks.example.com/"},
		{"renders empty", "{{ .Labels.missing }}", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &alertTranslator{
				extraLabels:        map[string]string{"env": "prod"},
				runbookURLTemplate: tt.template,
			}
			if err := tr.validate(); err != nil {
				t.Fatalf("validate() error = %v", err)
			}

			got, err := tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
				Id:  1,
				Uei: "uei.opennms.org/nodes/nodeDown",
				NodeCriteria: pb.NodeCriteria_builder{
					NodeLabel: "router1",
				}.Build(),
				Severity:             uint32(pb.Severity_MAJOR),
				FirstEventTime:       uint64(now.UnixMilli()),
				LastEventTime:        uint64(now.UnixMilli()),
				OperatorInstructions: tt.instructions,
			}.Build(), now: now, instanceName: "OpenNMS"})
			if err != nil {
				t.Fatalf("alarm() error = %v", err)
			}

			if runbook, ok := got.Annotations["runbook_url"]; runbook != tt.want || ok != (tt.want != "") {
				t.Errorf("runbook_url = %q (set %v), want %q", runbook, ok, tt.want)
			}
		})
	}
}

func TestAlertTranslatorHeartbeatRunbookURLTemplate(t *testing.T) {
	tr := &alertTranslator{runbookURLTemplate: "https://runbooks.example.com/{{ .Labels.alertname }}"}
	if err := tr.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}

	got := tr.heartbeat("OpenNMS", time.Now())

	if want := "https://runbooks.example.com/OpenNMSHeartbeat"; got.Annotations["runbook_url"] != want {
		t.Errorf("runbook_url = %q, want %q", got.Annotations["runbook_url"], want)
	}
}

func TestAlertTranslatorValidateRunbookURLTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantErr  string
	}{
		{"none", "", ""},
		{"valid", "https://runbooks.example.com/{{ pathEscape .Labels.alertname }}", ""},
		{"parse error", "https://runbooks.example.com/{{ .Labels.alertname", "invalid runbook URL template: "},
		{"unknown field", "https://runbooks.example.com/{{ .Lables.alertname }}", "invalid runbook URL template: "},
		{"unknown function", "https://runbooks.example.com/{{ lower .Labels.alertname }}", "invalid runbook URL template: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&alertTranslator{runbookURLTemplate: tt.template}).validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("validate() error = %v, want prefix %q", err, tt.wantErr)
			}
		})
	}
}

// fullAlarm returns an alarm with every field used for labels and annotations
// set
func fullAlarm(now time.Time) *pb.Alarm {
	return pb.Alarm_builder{
		Id:  25,
		Uei: "uei.opennms.org/nodes/nodeLostService",
		NodeCriteria: pb.NodeCriteria_builder{
			Id:            12,
			ForeignSource: "customer-a",
			ForeignId:     "1234",
			NodeLabel:     "router1",
			Location:      "Perth",
		}.Build(),
		IpAddress:            "10.0.0.1",
		ServiceName:          "ICMP",
		Severity:             uint32(pb.Severity_MAJOR),
		FirstEventTime:       uint64(now.UnixMilli()),
		LastEventTime:        uint64(now.UnixMilli()),
		LogMessage:           "ICMP outage identified on interface 10.0.0.1.",
		Description:          "<p>A ICMP outage was identified.</p>",
		OperatorInstructions: "Check the router, see https://wiki.example.com/runbooks/icmp",
	}.Build()
}

func TestAlertTranslatorAlarmExtraLabelsAndAnnotations(t *testing.T) {
	now := time.Now()

	tr := &alertTranslator{
		extraLabels:      map[string]string{"env": "prod", "team": "noc"},
		extraAnnotations: map[string]string{"dashboard": "https://grafana/d/onms"},
	}

	got, err := tr.alarm(instanceAlarm{alarm: fullAlarm(now), now: now, instanceName: "OpenNMS"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}

	wantLabels := map[string]string{
		"alertname":      "uei.opennms.org/nodes/nodeLostService",
		"alarm_id":       "25",
		"node_name":      "router1",
		"foreign_source": "customer-a",
		"instance_name":  "OpenNMS",
		"severity":       "major",
		"service":        "ICMP",
		"ip_address":     "10.0.0.1",
		"site":           "Perth",
		"env":            "prod",
		"team":           "noc",
	}
	if !reflect.DeepEqual(map[string]string(got.Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got.Labels, wantLabels)
	}

	wantAnnotations := map[string]string{
		"summary":               "ICMP outage identified on interface 10.0.0.1.",
		"description":           "A ICMP outage was identified.",
		"runbook_url":           "https://wiki.example.com/runbooks/icmp",
		"operator_instructions": "Check the router, see https://wiki.example.com/runbooks/icmp",
		"dashboard":             "https://grafana/d/onms",
	}
	if !reflect.DeepEqual(map[string]string(got.Annotations), wantAnnotations) {
		t.Errorf("annotations = %v, want %v", got.Annotations, wantAnnotations)
	}

	// an alarm with no annotations of its own still gets the extra ones
	got, err = tr.alarm(instanceAlarm{alarm: pb.Alarm_builder{
		Id:             2,
		Uei:            "uei.opennms.org/nodes/nodeDown",
		Severity:       uint32(pb.Severity_MAJOR),
		FirstEventTime: uint64(now.UnixMilli()),
		LastEventTime:  uint64(now.UnixMilli()),
	}.Build(), now: now, instanceName: "OpenNMS"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}
	if got.Labels["env"] != "prod" || got.Labels["team"] != "noc" {
		t.Errorf("labels = %v, want env and team", got.Labels)
	}
	if !reflect.DeepEqual(map[string]string(got.Annotations), map[string]string{"dashboard": "https://grafana/d/onms"}) {
		t.Errorf("annotations = %v, want only dashboard", got.Annotations)
	}
}

func TestAlertTranslatorHeartbeat(t *testing.T) {
	now := time.Now()

	tr := &alertTranslator{
		extraLabels:      map[string]string{"env": "prod"},
		extraAnnotations: map[string]string{"dashboard": "https://grafana/d/onms"},
		resolveTimeout:   time.Minute * 3,
	}

	got := tr.heartbeat("OpenNMS", now)

	wantLabels := map[string]string{
		"alertname":     "OpenNMSHeartbeat",
		"instance_name": "OpenNMS",
		"env":           "prod",
	}
	if !reflect.DeepEqual(map[string]string(got.Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got.Labels, wantLabels)
	}
	if !reflect.DeepEqual(map[string]string(got.Annotations), map[string]string{"dashboard": "https://grafana/d/onms"}) {
		t.Errorf("annotations = %v, want only dashboard", got.Annotations)
	}
	if startsAt := time.Time(got.StartsAt); !startsAt.Equal(now) {
		t.Errorf("StartsAt = %v, want %v", startsAt, now)
	}
	if endsAt := time.Time(got.EndsAt); !endsAt.Equal(now.Add(time.Minute * 3)) {
		t.Errorf("EndsAt = %v, want %v", endsAt, now.Add(time.Minute*3))
	}
}

// TestBuiltinNames ensures every label and annotation the receiver can set
// itself is listed as built-in, so it is rejected as an extra label or
// annotation
func TestBuiltinNames(t *testing.T) {
	now := time.Now()

	tr := &alertTranslator{}

	alarm, err := tr.alarm(instanceAlarm{alarm: fullAlarm(now), now: now, instanceName: "OpenNMS"})
	if err != nil {
		t.Fatalf("alarm() error = %v", err)
	}

	for _, a := range []map[string]string{alarm.Labels, tr.heartbeat("OpenNMS", now).Labels} {
		for name := range a {
			if !slices.Contains(builtinLabels, name) {
				t.Errorf("label %q is not listed in builtinLabels", name)
			}
		}
	}
	for name := range alarm.Annotations {
		if !slices.Contains(builtinAnnotations, name) {
			t.Errorf("annotation %q is not listed in builtinAnnotations", name)
		}
	}
}

func TestAlertTranslatorValidateLabels(t *testing.T) {
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
			err := (&alertTranslator{extraLabels: tt.labels}).validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAlertTranslatorValidateAnnotations(t *testing.T) {
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
			err := (&alertTranslator{extraAnnotations: tt.annotations}).validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
