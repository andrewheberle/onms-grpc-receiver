package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// ready returns the status code and body of the readiness check of s
func ready(t *testing.T, s *ServiceSyncServer) (int, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	s.HealthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/-/ready", nil))

	return rec.Code, rec.Body.String()
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		readiness readiness
		wantCode  int
		wantBody  string
	}{
		{starting, http.StatusServiceUnavailable, "Not ready: starting"},
		{serving, http.StatusOK, "Ready"},
		{stopping, http.StatusServiceUnavailable, "Not ready: stopping"},
		{stopped, http.StatusServiceUnavailable, "Not ready: stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.readiness.String(), func(t *testing.T) {
			s := must(NewServiceSyncServer())(t)
			s.setReadiness(tt.readiness)

			if code, body := ready(t, s); code != tt.wantCode || body != tt.wantBody {
				t.Errorf("readiness check = %d %q, want %d %q", code, body, tt.wantCode, tt.wantBody)
			}
		})
	}
}

// TestReadinessOnlyMovesForward checks a server that has started stopping
// cannot become ready again, such as when serving starts after Serve is
// cancelled
func TestReadinessOnlyMovesForward(t *testing.T) {
	tests := []struct {
		name  string
		moves []readiness
		want  readiness
	}{
		{"in order", []readiness{serving, stopping, stopped}, stopped},
		{"serving after stopping", []readiness{stopping, serving}, stopping},
		{"stopping after stopped", []readiness{stopped, stopping}, stopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := must(NewServiceSyncServer())(t)
			for _, r := range tt.moves {
				s.setReadiness(r)
			}

			if got := s.currentReadiness(); got != tt.want {
				t.Errorf("readiness = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestHealthServerNotServingWhenSet checks the gRPC health service reports
// the readiness as soon as it is set, as it otherwise reports the server as
// serving
func TestHealthServerNotServingWhenSet(t *testing.T) {
	s := must(NewServiceSyncServer())(t)
	h := health.NewServer()
	s.setHealthServer(h)

	for _, service := range healthServices {
		resp, err := h.Check(context.Background(), &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("Check(%q) error = %v", service, err)
		}
		if got := resp.GetStatus(); got != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("Check(%q) = %s, want NOT_SERVING while starting", service, got)
		}
	}
}

// waitFor polls cond until it is true, failing the test after 5 seconds
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestServeReadiness checks the readiness reported over HTTP and the gRPC
// health service as Serve starts, serves, stops while an alarm update stream
// is still open, and returns
func TestServeReadiness(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := must(NewServiceSyncServer(WithRegistry(reg)))(t)

	if code, body := ready(t, s); code != http.StatusServiceUnavailable || body != "Not ready: starting" {
		t.Errorf("readiness check before Serve = %d %q, want 503 starting", code, body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn, done := serve(t, s, ctx)
	healthClient := healthpb.NewHealthClient(conn)

	// serving
	waitFor(t, "readiness check to pass", func() bool {
		code, _ := ready(t, s)
		return code == http.StatusOK
	})
	for _, service := range healthServices {
		resp, err := healthClient.Check(context.Background(), &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("Check(%q) error = %v", service, err)
		}
		if got := resp.GetStatus(); got != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("Check(%q) = %s, want SERVING", service, got)
		}
	}

	// hold an alarm update stream open, so stopping waits for it
	stream, err := pb.NewNmsInventoryServiceSyncClient(conn).AlarmUpdate(context.Background())
	if err != nil {
		t.Fatalf("AlarmUpdate() error = %v", err)
	}
	if err := stream.Send(pb.AlarmUpdateList_builder{InstanceId: "test", InstanceName: "OpenNMS"}.Build()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	waitFor(t, "alarm update to be received", func() bool {
		return metricValue(t, reg, "onmsgrpc_alarm_total", "instance_id", "test") == 1
	})

	// watch health from before stopping, as no new calls are accepted once
	// stopping starts
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	watch, err := healthClient.Watch(watchCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if resp, err := watch.Recv(); err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Watch() first status = %v, %v, want SERVING", resp, err)
	}

	// stopping
	cancel()
	if resp, err := watch.Recv(); err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("Watch() status after cancel = %v, %v, want NOT_SERVING", resp, err)
	}
	stopWatch()
	if code, body := ready(t, s); code != http.StatusServiceUnavailable || body != "Not ready: stopping" {
		t.Errorf("readiness check while stopping = %d %q, want 503 stopping", code, body)
	}

	// stopped once the open stream finishes
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("Recv() error = %v, want io.EOF", err)
	}
	waitServed(t, done)

	if code, body := ready(t, s); code != http.StatusServiceUnavailable || body != "Not ready: stopped" {
		t.Errorf("readiness check after Serve = %d %q, want 503 stopped", code, body)
	}
}

// TestServeFailedReadiness checks a server whose serving fails is not ready
func TestServeFailedReadiness(t *testing.T) {
	s := must(NewServiceSyncServer())(t)

	lis := bufconn.Listen(1024)
	_ = lis.Close()

	if err := s.Serve(context.Background(), lis); err == nil {
		t.Fatal("Serve() error = nil, want error from the closed listener")
	}

	if code, body := ready(t, s); code != http.StatusServiceUnavailable || body != "Not ready: stopped" {
		t.Errorf("readiness check = %d %q, want 503 stopped", code, body)
	}
	resp, err := s.health.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := resp.GetStatus(); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("Check() = %s, want NOT_SERVING", got)
	}
}
