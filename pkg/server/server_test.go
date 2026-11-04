package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	"github.com/prometheus/client_golang/prometheus/collectors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

// must returns a function that returns v, failing the test if err is not
// nil, for constructors that return an error
func must[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		return v
	}
}

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

func (h *heartbeatStream) Context() context.Context {
	return context.Background()
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

// TestNewServiceSyncServerRejectsConfig checks each invalid setting stops the
// server being created, rather than failing later
func TestNewServiceSyncServerRejectsConfig(t *testing.T) {
	goRegistry := func() *prometheus.Registry {
		reg := prometheus.NewRegistry()
		reg.MustRegister(collectors.NewGoCollector())
		return reg
	}

	tests := []struct {
		name    string
		opts    []ServiceSyncServerOption
		wantErr string
	}{
		{"nil logger", []ServiceSyncServerOption{WithLogger(nil)}, "invalid logger: must not be nil"},
		{"nil registry", []ServiceSyncServerOption{WithRegistry(nil)}, "invalid registry: must not be nil"},
		{"registry with go collector", []ServiceSyncServerOption{WithRegistry(goRegistry())}, "registering metrics"},
		{"empty alertmanager URLs", []ServiceSyncServerOption{WithAlertmanagerUrl([]string{})}, "invalid alertmanager URLs: none given"},
		{"alertmanager URL without scheme", []ServiceSyncServerOption{WithAlertmanagerUrl([]string{"am:9093"})}, `invalid alertmanager URL "am:9093": must be http or https`},
		{"alertmanager URL listed twice", []ServiceSyncServerOption{WithAlertmanagerUrl([]string{"http://am:9093", "http://am:9093/"})}, `invalid alertmanager URL "http://am:9093/": listed more than once`},
		{"alertmanager scheme", []ServiceSyncServerOption{WithAlertManagerSrv("htps", "_alertmanager._tcp.example.com")}, `invalid alertmanager scheme "htps": must be http or https`},
		{"empty SRV record", []ServiceSyncServerOption{WithAlertManagerSrv("http", "")}, "invalid alertmanager SRV record: must not be empty"},
		{"negative SRV cache TTL", []ServiceSyncServerOption{WithSRVCacheTTL(-time.Second)}, "invalid SRV cache TTL -1s: must not be negative"},
		{"invalid header", []ServiceSyncServerOption{WithHeaders(map[string]string{"X Scope": "noc"})}, `invalid header "X Scope"`},
		{"reserved header", []ServiceSyncServerOption{WithHeaders(map[string]string{"Host": "am"})}, `invalid header "Host": Host cannot be set`},
		{"URL map with query", []ServiceSyncServerOption{WithURLMapping(map[string]string{"6f8a7c1e-uuid": "http://horizon:8980/opennms/?x=1"})}, `invalid URL for instance "6f8a7c1e-uuid" in URL map "http://horizon:8980/opennms/?x=1": must not have a query`},
		{"unparseable URL map", []ServiceSyncServerOption{WithURLMapping(map[string]string{"6f8a7c1e-uuid": "http://horizon 1:8980/opennms/"})}, `invalid URL for instance "6f8a7c1e-uuid" in URL map "http://horizon 1:8980/opennms/": invalid character " " in host name`},
		{"URL map with empty instance ID", []ServiceSyncServerOption{WithURLMapping(map[string]string{"": "http://horizon:8980/opennms/"})}, "invalid URL map: an instance ID is empty"},
		{"flush threshold", []ServiceSyncServerOption{WithFlushThreshold(0)}, "invalid flush threshold 0: must be at least 1"},
		{"flush interval", []ServiceSyncServerOption{WithFlushInterval(0)}, "invalid flush interval 0s: must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServiceSyncServer(tt.opts...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewServiceSyncServer() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestNewServiceSyncServerReportsEveryProblem checks every problem with the
// configuration is reported at once, from each part of the server
func TestNewServiceSyncServerReportsEveryProblem(t *testing.T) {
	_, err := NewServiceSyncServer(
		WithAlertLabels(map[string]string{"severity": "critical"}),
		WithResolveHold(-time.Minute),
		WithURLMapping(map[string]string{"6f8a7c1e-uuid": "horizon:8980"}),
		WithAlertmanagerUrl([]string{"am:9093", "ftp://am:9093"}),
		WithHeaders(map[string]string{"Content-Type": "text/plain"}),
		WithFlushInterval(-time.Second),
	)
	if err == nil {
		t.Fatal("NewServiceSyncServer() error = nil, want every problem")
	}

	for _, want := range []string{
		`"severity" clashes with a built-in label`,
		"invalid resolve hold -1m0s",
		`invalid URL for instance "6f8a7c1e-uuid" in URL map`,
		`invalid alertmanager URL "am:9093"`,
		`invalid alertmanager URL "ftp://am:9093"`,
		`invalid header "Content-Type"`,
		"invalid flush interval -1s",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("NewServiceSyncServer() error = %v, want containing %q", err, want)
		}
	}
}

// TestNewServiceSyncServerLogOnly checks a warning is logged at startup when
// no Alertmanager is configured, as alarms are then only logged
func TestNewServiceSyncServerLogOnly(t *testing.T) {
	tests := []struct {
		name     string
		opts     []ServiceSyncServerOption
		wantWarn bool
	}{
		{"no alertmanager", nil, true},
		{"alertmanager URL", []ServiceSyncServerOption{WithAlertmanagerUrl([]string{"http://am:9093"})}, false},
		{"alertmanager SRV record", []ServiceSyncServerOption{WithAlertManagerSrv("http", "_alertmanager._tcp.example.com")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

			if _, err := NewServiceSyncServer(append(tt.opts, WithLogger(logger))...); err != nil {
				t.Fatalf("NewServiceSyncServer() error = %v", err)
			}

			if got := strings.Contains(logs.String(), "no Alertmanager configured"); got != tt.wantWarn {
				t.Errorf("warned = %v, want %v, logs %q", got, tt.wantWarn, logs.String())
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

// TestServeCancelledBeforeServing checks Serve returns no error when ctx is
// cancelled before gRPC has started serving
func TestServeCancelledBeforeServing(t *testing.T) {
	s, err := NewServiceSyncServer()
	if err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Serve(ctx, bufconn.Listen(1024*1024)); err != nil {
		t.Errorf("Serve() error = %v, want nil", err)
	}
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
	s.pipeline.heartbeat(context.Background(), pb.HeartBeat_builder{
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
		"onmsgrpc_alertmanager_retries_total":            "Total number of requests to alertmanager that were retried after a failure.",
		"onmsgrpc_alertmanager_lookup_error_total":       "Total number of errors during lookups of Alertmanagers.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
}
