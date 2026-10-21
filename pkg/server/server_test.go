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
