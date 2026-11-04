package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/test/bufconn"
)

// frameTypePing is the HTTP/2 frame type of a ping
const frameTypePing = 0x6

// TestServeClosesUnansweredConnections checks a connection that stops
// answering, as one from a Horizon instance that has gone away does, is
// pinged and then closed, rather than held open
func TestServeClosesUnansweredConnections(t *testing.T) {
	saved := keepaliveParams
	keepaliveParams = keepalive.ServerParameters{Time: 100 * time.Millisecond, Timeout: 100 * time.Millisecond}
	t.Cleanup(func() { keepaliveParams = saved })

	s := must(NewServiceSyncServer())(t)

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, lis)
	}()
	t.Cleanup(func() {
		cancel()
		waitServed(t, done)
	})

	conn, err := lis.Dial()
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	// an HTTP/2 client that sends its preface and empty settings, then
	// answers nothing
	if _, err := conn.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")); err != nil {
		t.Fatalf("writing preface error = %v", err)
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0x4, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("writing settings error = %v", err)
	}

	// read frames until the server closes the connection, which it does
	// without a GOAWAY once a ping goes unanswered
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var pinged bool
	for {
		header := make([]byte, 9)
		if _, err = io.ReadFull(conn, header); err != nil {
			break
		}
		if header[3] == frameTypePing {
			pinged = true
		}
		length := int64(header[0])<<16 | int64(header[1])<<8 | int64(header[2])
		if _, err = io.CopyN(io.Discard, conn, length); err != nil {
			break
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("connection still open, want it closed once its ping went unanswered")
	}
	if !pinged {
		t.Error("connection closed without being pinged, want it pinged first")
	}
}
