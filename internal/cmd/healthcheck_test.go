package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestDialAddress(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{":8080", "localhost:8080"},
		{"0.0.0.0:8080", "localhost:8080"},
		{"[::]:8080", "localhost:8080"},
		{"localhost:8080", "localhost:8080"},
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"10.1.2.3:8080", "10.1.2.3:8080"},
		{"[::1]:8080", "[::1]:8080"},
		{"receiver.example.com:8080", "receiver.example.com:8080"},
		{"no-port", "no-port"},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			if got := dialAddress(tt.address); got != tt.want {
				t.Errorf("dialAddress(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

// writeSelfSignedCert writes a self-signed certificate and its key to dir,
// returning their paths
func writeSelfSignedCert(t *testing.T, dir string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "receiver.example.com"},
		DNSNames:     []string{"receiver.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}

	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing certificate error = %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("writing key error = %v", err)
	}

	return certPath, keyPath
}

// runSpog runs a receiver with args until the test ends, waiting until it
// is serving
func runSpog(t *testing.T, useTLS bool, args ...string) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, append([]string{"spog", "--silent"}, args...))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("receiver error = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("receiver did not stop")
		}
	})

	address := args[len(args)-1]
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := checkHealth(context.Background(), address, useTLS)
		if status == healthpb.HealthCheckResponse_SERVING {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("receiver at %s did not start serving", address)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runHealthServer serves only the gRPC health service, with the given
// overall status, until the test ends, returning its address
func runHealthServer(t *testing.T, status healthpb.HealthCheckResponse_ServingStatus) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	h := health.NewServer()
	h.SetServingStatus("", status)
	s := grpc.NewServer()
	healthpb.RegisterHealthServer(s, h)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(s.Stop)

	return l.Addr().String()
}

func TestHealthcheck(t *testing.T) {
	certPath, keyPath := writeSelfSignedCert(t, t.TempDir())

	tests := []struct {
		name    string
		setup   func(t *testing.T) []string
		wantErr string
	}{
		{"serving", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, false, "--address", addr)
			return []string{"--address", addr}
		}, ""},
		{"from environment variables", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, false, "--address", addr)
			_, port, _ := net.SplitHostPort(addr)

			// as in the container image, which also sets other variables
			t.Setenv("ONMS_GRPC_ADDRESS", ":"+port)
			t.Setenv("ONMS_GRPC_METRICS_ADDRESS", ":0")
			return nil
		}, ""},
		{"tls", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, true, "--cert", certPath, "--key", keyPath, "--address", addr)
			return []string{"--address", addr, "--cert", certPath}
		}, ""},
		{"tls without cert", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, true, "--cert", certPath, "--key", keyPath, "--address", addr)
			return []string{"--address", addr, "--timeout", "500ms"}
		}, "checking health of"},
		{"not serving", func(t *testing.T) []string {
			return []string{"--address", runHealthServer(t, healthpb.HealthCheckResponse_NOT_SERVING)}
		}, "is NOT_SERVING"},
		{"nothing listening", func(t *testing.T) []string {
			return []string{"--address", freeAddress(t), "--timeout", "500ms"}
		}, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.setup(t)

			err := Execute(context.Background(), append([]string{"healthcheck"}, args...))
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Execute() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Execute() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestHealthcheckDefaultAddress checks that with no flags or environment
// variables, the receiver at the default address of spog is checked
func TestHealthcheckDefaultAddress(t *testing.T) {
	for _, env := range os.Environ() {
		if name, _, _ := strings.Cut(env, "="); strings.HasPrefix(name, "ONMS_GRPC_") {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}

	// something may be listening on the default address, in which case the
	// check either passes or fails for that receiver
	err := Execute(context.Background(), []string{"healthcheck", "--timeout", "500ms"})
	if err != nil && !strings.Contains(err.Error(), "checking health of "+defaultAddress) {
		t.Errorf("Execute() error = %v, want the default address %s checked", err, defaultAddress)
	}
}
