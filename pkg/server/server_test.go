package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.pipeline.run(ctx)
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
	<-done

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

// serve runs s.Serve on an in-memory listener until the test ends, returning
// a client connection to it and a channel that receives the result of Serve
func serve(t *testing.T, s *ServiceSyncServer, ctx context.Context) (*grpc.ClientConn, <-chan error) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })

	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, lis)
	}()

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn, done
}

// waitServed waits for Serve to return, failing the test if it does not
func waitServed(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return after being cancelled")
	}
}

func TestServeStopsWhenCancelled(t *testing.T) {
	s, err := NewServiceSyncServer()
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	_, done := serve(t, s, ctx)

	cancel()
	waitServed(t, done)
}

// TestServeFlushesAlarmsReceivedWhileStopping checks that an alarm update
// received on an open stream after Serve is cancelled still reaches
// Alertmanager, as gRPC is stopped before the alarms are flushed
func TestServeFlushesAlarmsReceivedWhileStopping(t *testing.T) {
	var got []models.PostableAlert
	reg := prometheus.NewRegistry()
	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{fakeAlertmanager(t, &got)}),
		WithRegistry(reg),
	)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn, done := serve(t, s, ctx)

	stream, err := pb.NewNmsInventoryServiceSyncClient(conn).AlarmUpdate(context.Background())
	if err != nil {
		t.Fatalf("AlarmUpdate() error = %v", err)
	}

	// make sure the stream is open before cancelling, as no new streams are
	// accepted once stopping has started
	if err := stream.Send(pb.AlarmUpdateList_builder{InstanceId: "test", InstanceName: "OpenNMS"}.Build()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for metricValue(t, reg, "onmsgrpc_alarm_total", "instance_id", "test") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first alarm update was not received")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()

	if err := stream.Send(pb.AlarmUpdateList_builder{
		InstanceId:   "test",
		InstanceName: "OpenNMS",
		Alarms:       []*pb.Alarm{fullAlarm(time.Now())},
	}.Build()); err != nil {
		t.Fatalf("Send() after cancel error = %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() error = %v, want io.EOF", err)
	}

	waitServed(t, done)

	if len(got) != 1 || got[0].Labels["alarm_id"] != "25" {
		t.Errorf("alertmanager got %v, want the alarm sent after cancelling", got)
	}
}

// TestServiceSyncServerMetrics checks the name and help of every metric the
// receiver exposes, as they are relied on by dashboards and alerts
func TestServiceSyncServerMetrics(t *testing.T) {
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(am.Close)

	reg := prometheus.NewRegistry()
	s, err := NewServiceSyncServer(
		WithAlertmanagerUrl([]string{am.URL}),
		WithRegistry(reg),
	)
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	// metrics with labels only have series once they are used
	now := time.Now()
	s.pipeline.heartbeat(pb.HeartBeat_builder{
		MonitoringInstance: pb.MonitoringInstance_builder{InstanceId: "test", InstanceName: "OpenNMS"}.Build(),
	}.Build(), now)
	s.pipeline.alarmUpdate(pb.AlarmUpdateList_builder{InstanceId: "test", Snapshot: true}.Build(), now)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	got := make(map[string]string)
	for _, family := range families {
		if strings.HasPrefix(family.GetName(), "onmsgrpc_") {
			got[family.GetName()] = family.GetHelp()
		}
	}

	want := map[string]string{
		"onmsgrpc_alarm_total":                           "Total number of alarm updates seen from a Horizon instance.",
		"onmsgrpc_alarm_count":                           "Current number of active alarms for a Horizon instance from the last full snapshot of alarms.",
		"onmsgrpc_alarm_last_snapshot_timestamp_seconds": "Unix time of the last full alarm snapshot received from a Horizon instance.",
		"onmsgrpc_heartbeat_total":                       "Total number of heartbeat updates seen from a Horizon instance.",
		"onmsgrpc_alarm_queue_depth":                     "Current number of alarm batches waiting in the queue.",
		"onmsgrpc_alarm_dropped_total":                   "Total number of alarms dropped due to the queue being full or the server stopping.",
		"onmsgrpc_alertmanager_total":                    "Total number of messages sent to alertmanager.",
		"onmsgrpc_alertmanager_failed_total":             "Total number of messages that could not be sent to alertmanager.",
		"onmsgrpc_alertmanager_lookup_error_total":       "Total number of errors during lookups of Alertmanagers.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
}
