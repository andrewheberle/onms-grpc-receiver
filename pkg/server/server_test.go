package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
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
