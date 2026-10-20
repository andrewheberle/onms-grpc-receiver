package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
