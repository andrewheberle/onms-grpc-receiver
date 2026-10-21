package server

import (
	"context"
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

// TestAlarmUpdate checks alarm updates received on the stream are passed to
// the alert pipeline and sent to alertmanager once flushed, the pipeline is
// covered by the alertPipeline tests
func TestAlarmUpdate(t *testing.T) {
	var got []models.PostableAlert
	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{fakeAlertmanager(t, &got)}),
	)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Start(ctx)
	}()

	stream := &alarmStream{updates: []*pb.AlarmUpdateList{pb.AlarmUpdateList_builder{
		InstanceId:   "6f8a7c1e-uuid",
		InstanceName: "OpenNMS",
		Alarms:       []*pb.Alarm{fullAlarm(time.Now())},
	}.Build()}}
	if err := s.AlarmUpdate(stream); err != nil {
		t.Fatalf("AlarmUpdate() error = %v", err)
	}

	// stopping flushes the queued alarms
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("AlarmUpdate() sent %d alerts, want 1", len(got))
	}
	if got[0].Labels["alarm_id"] != "25" {
		t.Errorf("labels = %v, want alarm_id 25", got[0].Labels)
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
