package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// freeAddr returns a local address that is free to listen on
func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find a free address: %v", err)
	}
	defer func() { _ = l.Close() }()

	return l.Addr().String()
}

// waitListening waits until addr accepts connections
func waitListening(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is not listening: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSpogFlushesAlarmsReceivedWhileStopping checks that an alarm update
// received on an open stream after shutdown has started still reaches
// Alertmanager, as gRPC is stopped before the alarms are flushed
func TestSpogFlushesAlarmsReceivedWhileStopping(t *testing.T) {
	var (
		mu  sync.Mutex
		got []models.PostableAlert
	)
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var alerts []models.PostableAlert
		if err := json.NewDecoder(r.Body).Decode(&alerts); err != nil {
			t.Errorf("could not decode alerts: %v", err)
		}
		mu.Lock()
		got = append(got, alerts...)
		mu.Unlock()
	}))
	defer am.Close()

	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"spog", "--silent", "--address", addr, "--alertmanager.url", am.URL})
	}()
	waitListening(t, addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	stream, err := pb.NewNmsInventoryServiceSyncClient(conn).AlarmUpdate(context.Background())
	if err != nil {
		t.Fatalf("AlarmUpdate() error = %v", err)
	}

	// make sure the stream is open before shutting down, as no new streams
	// are accepted once shutdown has started
	if err := stream.Send(pb.AlarmUpdateList_builder{InstanceId: "test", InstanceName: "OpenNMS"}.Build()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	cancel()
	time.Sleep(200 * time.Millisecond)

	now := uint64(time.Now().UnixMilli())
	if err := stream.Send(pb.AlarmUpdateList_builder{
		InstanceId:   "test",
		InstanceName: "OpenNMS",
		Alarms: []*pb.Alarm{pb.Alarm_builder{
			Id:             42,
			Uei:            "uei.opennms.org/nodes/nodeDown",
			Severity:       uint32(pb.Severity_MAJOR),
			FirstEventTime: now,
			LastEventTime:  now,
			LastUpdateTime: now,
		}.Build()},
	}.Build()); err != nil {
		t.Fatalf("Send() while stopping error = %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() error = %v, want io.EOF", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute() did not stop after being cancelled")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Labels["alarm_id"] != "42" {
		t.Errorf("alertmanager got %v, want the alarm sent while stopping", got)
	}
}
