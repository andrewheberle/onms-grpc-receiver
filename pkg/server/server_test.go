package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestHandleAlarms(t *testing.T) {
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
			var got []models.PostableAlert
			am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("could not decode alerts: %v", err)
				}
			}))
			defer am.Close()

			s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
			if err != nil {
				t.Fatalf("NewServiceSyncServer() error = %v", err)
			}

			s.handleAlarms([]instanceAlarm{{alarm: tt.alarm, now: now, instanceID: "test"}})

			if sent := len(got) == 1; sent != tt.want {
				t.Errorf("handleAlarms() sent = %v, want %v", sent, tt.want)
			}
		})
	}
}

func TestHandleAlarmsEndsAt(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) uint64 {
		return uint64(now.Add(d).UnixMilli())
	}

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{
		{alarm: pb.Alarm_builder{
			Id:             1,
			Severity:       uint32(pb.Severity_MAJOR),
			FirstEventTime: ms(-time.Hour),
			LastEventTime:  ms(-time.Hour),
			LastUpdateTime: ms(-time.Hour),
		}.Build(), now: now, instanceID: "test"},
		{alarm: pb.Alarm_builder{
			Id:             2,
			Severity:       uint32(pb.Severity_CLEARED),
			FirstEventTime: ms(-time.Hour),
			LastEventTime:  ms(-time.Hour),
			LastUpdateTime: ms(-time.Second),
		}.Build(), now: now, instanceID: "test"},
	})

	if len(got) != 2 {
		t.Fatalf("handleAlarms() sent %d alerts, want 2", len(got))
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Labels["alarm_id"] < got[j].Labels["alarm_id"] })

	if endsAt := time.Time(got[0].EndsAt); !endsAt.After(now) {
		t.Errorf("firing alert EndsAt = %v, want after %v", endsAt, now)
	}
	if endsAt := time.Time(got[1].EndsAt); endsAt.After(time.Now()) {
		t.Errorf("cleared alert EndsAt = %v, want in the past", endsAt)
	}
}

func TestHandleAlarmsLabelsAndAnnotations(t *testing.T) {
	now := time.Now()

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{{alarm: pb.Alarm_builder{
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
	}.Build(), now: now, instanceID: "6f8a7c1e-uuid", instanceName: "OpenNMS"}})

	if len(got) != 1 {
		t.Fatalf("handleAlarms() sent %d alerts, want 1", len(got))
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
	if !reflect.DeepEqual(map[string]string(got[0].Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got[0].Labels, wantLabels)
	}

	wantAnnotations := map[string]string{
		"summary":               "ICMP outage identified on interface 10.0.0.1.",
		"description":           "A ICMP outage was identified on interface 10.0.0.1. A new Outage record has been created & service level availability calculations will be impacted.",
		"operator_instructions": "Check the router.",
	}
	if !reflect.DeepEqual(map[string]string(got[0].Annotations), wantAnnotations) {
		t.Errorf("annotations = %v, want %v", got[0].Annotations, wantAnnotations)
	}
}

func TestHandleAlarmsOmitsEmptyAnnotations(t *testing.T) {
	now := time.Now()

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{{alarm: pb.Alarm_builder{
		Id:             1,
		Uei:            "uei.opennms.org/nodes/nodeDown",
		Severity:       uint32(pb.Severity_MAJOR),
		FirstEventTime: uint64(now.UnixMilli()),
		LastEventTime:  uint64(now.UnixMilli()),
		Description:    "<p> </p>",
	}.Build(), now: now, instanceName: "OpenNMS"}})

	if len(got) != 1 {
		t.Fatalf("handleAlarms() sent %d alerts, want 1", len(got))
	}
	if len(got[0].Annotations) != 0 {
		t.Errorf("annotations = %v, want none", got[0].Annotations)
	}
	if _, ok := got[0].Labels["foreign_source"]; ok {
		t.Errorf("labels = %v, want no foreign_source", got[0].Labels)
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

func TestHandleAlarmsRunbookURL(t *testing.T) {
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
			var got []models.PostableAlert
			am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("could not decode alerts: %v", err)
				}
			}))
			defer am.Close()

			s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
			if err != nil {
				t.Fatalf("NewServiceSyncServer() error = %v", err)
			}

			s.handleAlarms([]instanceAlarm{{alarm: pb.Alarm_builder{
				Id:                   1,
				Uei:                  "uei.opennms.org/nodes/nodeDown",
				Severity:             uint32(pb.Severity_MAJOR),
				FirstEventTime:       uint64(now.UnixMilli()),
				LastEventTime:        uint64(now.UnixMilli()),
				OperatorInstructions: tt.instructions,
			}.Build(), now: now, instanceName: "OpenNMS"}})

			if len(got) != 1 {
				t.Fatalf("handleAlarms() sent %d alerts, want 1", len(got))
			}
			if !reflect.DeepEqual(map[string]string(got[0].Annotations), tt.want) {
				t.Errorf("annotations = %v, want %v", got[0].Annotations, tt.want)
			}
			if _, ok := got[0].Labels["runbook_url"]; ok {
				t.Errorf("labels = %v, want no runbook_url label", got[0].Labels)
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

func TestHandleAlarmsAlertLabelsAndAnnotations(t *testing.T) {
	now := time.Now()

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{am.URL}),
		WithAlertLabels(map[string]string{"env": "prod", "team": "noc"}),
		WithAlertAnnotations(map[string]string{"dashboard": "https://grafana/d/onms"}),
	)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{
		{alarm: fullAlarm(now), now: now, instanceName: "OpenNMS"},
		{alarm: pb.Alarm_builder{
			Id:             2,
			Uei:            "uei.opennms.org/nodes/nodeDown",
			Severity:       uint32(pb.Severity_MAJOR),
			FirstEventTime: uint64(now.UnixMilli()),
			LastEventTime:  uint64(now.UnixMilli()),
		}.Build(), now: now, instanceName: "OpenNMS"},
	})

	if len(got) != 2 {
		t.Fatalf("handleAlarms() sent %d alerts, want 2", len(got))
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Labels["alarm_id"] > got[j].Labels["alarm_id"] })

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
	if !reflect.DeepEqual(map[string]string(got[0].Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got[0].Labels, wantLabels)
	}

	wantAnnotations := map[string]string{
		"summary":               "ICMP outage identified on interface 10.0.0.1.",
		"description":           "A ICMP outage was identified.",
		"runbook_url":           "https://wiki.example.com/runbooks/icmp",
		"operator_instructions": "Check the router, see https://wiki.example.com/runbooks/icmp",
		"dashboard":             "https://grafana/d/onms",
	}
	if !reflect.DeepEqual(map[string]string(got[0].Annotations), wantAnnotations) {
		t.Errorf("annotations = %v, want %v", got[0].Annotations, wantAnnotations)
	}

	// an alarm with no annotations of its own still gets the extra ones
	if got[1].Labels["env"] != "prod" || got[1].Labels["team"] != "noc" {
		t.Errorf("labels = %v, want env and team", got[1].Labels)
	}
	if !reflect.DeepEqual(map[string]string(got[1].Annotations), map[string]string{"dashboard": "https://grafana/d/onms"}) {
		t.Errorf("annotations = %v, want only dashboard", got[1].Annotations)
	}
}

// heartbeatStream is a minimal HeartBeatUpdate stream that returns the given
// heartbeats then io.EOF
type heartbeatStream struct {
	grpc.BidiStreamingServer[pb.HeartBeat, emptypb.Empty]
	heartbeats []*pb.HeartBeat
}

func (h *heartbeatStream) Recv() (*pb.HeartBeat, error) {
	if len(h.heartbeats) == 0 {
		return nil, io.EOF
	}
	hb := h.heartbeats[0]
	h.heartbeats = h.heartbeats[1:]

	return hb, nil
}

func sendHeartbeat(t *testing.T, opts ...ServiceSyncServerOption) []models.PostableAlert {
	t.Helper()

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(append([]ServiceSyncServerOption{WithAlertmanagerUrl([]string{am.URL})}, opts...)...)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	stream := &heartbeatStream{heartbeats: []*pb.HeartBeat{pb.HeartBeat_builder{
		MonitoringInstance: pb.MonitoringInstance_builder{
			InstanceId:   "6f8a7c1e-uuid",
			InstanceName: "OpenNMS",
		}.Build(),
		Message: "heartbeat",
	}.Build()}}
	if err := s.HeartBeatUpdate(stream); err != nil {
		t.Fatalf("HeartBeatUpdate() error = %v", err)
	}

	return got
}

func TestHeartBeatUpdateAlertLabelsAndAnnotations(t *testing.T) {
	got := sendHeartbeat(t,
		WithAlertLabels(map[string]string{"env": "prod"}),
		WithAlertAnnotations(map[string]string{"dashboard": "https://grafana/d/onms"}),
	)

	if len(got) != 1 {
		t.Fatalf("HeartBeatUpdate() sent %d alerts, want 1", len(got))
	}

	wantLabels := map[string]string{
		"alertname":     "OpenNMSHeartbeat",
		"instance_name": "OpenNMS",
		"env":           "prod",
	}
	if !reflect.DeepEqual(map[string]string(got[0].Labels), wantLabels) {
		t.Errorf("labels = %v, want %v", got[0].Labels, wantLabels)
	}
	if !reflect.DeepEqual(map[string]string(got[0].Annotations), map[string]string{"dashboard": "https://grafana/d/onms"}) {
		t.Errorf("annotations = %v, want only dashboard", got[0].Annotations)
	}
}

// TestBuiltinNames ensures every label and annotation the receiver can set
// itself is listed as built-in, so it is rejected as an alert label or
// annotation
func TestBuiltinNames(t *testing.T) {
	now := time.Now()

	var got []models.PostableAlert
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	defer am.Close()

	s, err := NewServiceSyncServer(WithAlertmanagerUrl([]string{am.URL}))
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{{alarm: fullAlarm(now), now: now, instanceName: "OpenNMS"}})
	if len(got) != 1 {
		t.Fatalf("handleAlarms() sent %d alerts, want 1", len(got))
	}
	alerts := append(got, sendHeartbeat(t)...)

	for _, a := range alerts {
		for name := range a.Labels {
			if !slices.Contains(builtinLabels, name) {
				t.Errorf("label %q is not listed in builtinLabels", name)
			}
		}
		for name := range a.Annotations {
			if !slices.Contains(builtinAnnotations, name) {
				t.Errorf("annotation %q is not listed in builtinAnnotations", name)
			}
		}
	}
}
