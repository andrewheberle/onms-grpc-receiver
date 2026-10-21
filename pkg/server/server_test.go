package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// fakeAlertmanager returns the URL of an Alertmanager that stores the alerts
// it was last sent in got
func fakeAlertmanager(t *testing.T, got *[]models.PostableAlert) string {
	t.Helper()

	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
	}))
	t.Cleanup(am.Close)

	return am.URL
}

// TestHandleAlarms checks translated alarms are sent to alertmanager, the
// details of each alert are covered by the alertTranslator tests
func TestHandleAlarms(t *testing.T) {
	now := time.Now()

	var got []models.PostableAlert
	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{fakeAlertmanager(t, &got)}),
		WithAlertLabels(map[string]string{"env": "prod"}),
	)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	s.handleAlarms([]instanceAlarm{
		{alarm: fullAlarm(now), now: now, instanceName: "OpenNMS"},
		{alarm: pb.Alarm_builder{
			Id:             2,
			Severity:       uint32(pb.Severity_NORMAL),
			FirstEventTime: uint64(now.UnixMilli()),
			LastEventTime:  uint64(now.UnixMilli()),
		}.Build(), now: now, instanceName: "OpenNMS"},
	})

	if len(got) != 1 {
		t.Fatalf("handleAlarms() sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alarm_id"] != "25" || got[0].Labels["env"] != "prod" {
		t.Errorf("labels = %v, want alarm_id 25 and env prod", got[0].Labels)
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

// alarmStream is a minimal AlarmUpdate stream that returns the given updates
// then io.EOF
type alarmStream struct {
	grpc.BidiStreamingServer[pb.AlarmUpdateList, emptypb.Empty]
	updates []*pb.AlarmUpdateList
}

func (a *alarmStream) Recv() (*pb.AlarmUpdateList, error) {
	if len(a.updates) == 0 {
		return nil, io.EOF
	}
	u := a.updates[0]
	a.updates = a.updates[1:]

	return u, nil
}

// TestAlarmUpdateLastSnapshot checks the last snapshot time is only recorded
// for full snapshots, per instance
func TestAlarmUpdateLastSnapshot(t *testing.T) {
	update := func(id string, snapshot bool, alarms int) *pb.AlarmUpdateList {
		list := make([]*pb.Alarm, alarms)
		for i := range list {
			list[i] = pb.Alarm_builder{Id: uint64(i + 1)}.Build()
		}
		return pb.AlarmUpdateList_builder{
			InstanceId: id,
			Snapshot:   snapshot,
			Alarms:     list,
		}.Build()
	}

	tests := []struct {
		name    string
		updates []*pb.AlarmUpdateList
		want    []string
	}{
		{"snapshot", []*pb.AlarmUpdateList{update("a", true, 2)}, []string{"a"}},
		{"not a snapshot", []*pb.AlarmUpdateList{update("a", false, 2)}, nil},
		{"empty snapshot", []*pb.AlarmUpdateList{update("a", true, 0)}, []string{"a"}},
		{"two instances", []*pb.AlarmUpdateList{
			update("a", true, 1),
			update("b", false, 1),
			update("c", true, 1),
		}, []string{"a", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewServiceSyncServer()
			if err != nil {
				t.Fatalf("NewServiceSyncServer() error = %v", err)
			}

			start := time.Now().Unix()
			if err := s.AlarmUpdate(&alarmStream{updates: tt.updates}); err != nil {
				t.Fatalf("AlarmUpdate() error = %v", err)
			}
			end := time.Now().Unix() + 1

			if got := testutil.CollectAndCount(s.alarmLastSnapshot); got != len(tt.want) {
				t.Fatalf("last snapshot series = %d, want %d", got, len(tt.want))
			}
			for _, id := range tt.want {
				got := testutil.ToFloat64(s.alarmLastSnapshot.WithLabelValues(id))
				if got < float64(start) || got > float64(end) {
					t.Errorf("last snapshot for %q = %v, want between %d and %d", id, got, start, end)
				}
			}
		})
	}
}

// TestHeartBeatUpdate checks translated heartbeats are sent to alertmanager,
// the details of each alert are covered by the alertTranslator tests
func TestHeartBeatUpdate(t *testing.T) {
	var got []models.PostableAlert
	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{fakeAlertmanager(t, &got)}),
		WithAlertLabels(map[string]string{"env": "prod"}),
	)
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

	if len(got) != 1 {
		t.Fatalf("HeartBeatUpdate() sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alertname"] != "OpenNMSHeartbeat" || got[0].Labels["env"] != "prod" {
		t.Errorf("labels = %v, want alertname OpenNMSHeartbeat and env prod", got[0].Labels)
	}
}

func TestNewServiceSyncServerRejectsBuiltinNames(t *testing.T) {
	tests := []struct {
		name    string
		opt     ServiceSyncServerOption
		wantErr string
	}{
		{"label", WithAlertLabels(map[string]string{"severity": "critical"}), `"severity" clashes with a built-in label`},
		{"annotation", WithAlertAnnotations(map[string]string{"summary": "x"}), `"summary" clashes with a built-in annotation`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServiceSyncServer(tt.opt)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewServiceSyncServer() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
