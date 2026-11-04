package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
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

// testCert is a certificate and key written to files for a test
type testCert struct {
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPath string
	keyPath  string
}

// writeCert writes a certificate for names to dir as name.crt and name.key,
// issued by issuer, or self-signed if issuer is nil. A CA certificate is
// written if ca is set.
func writeCert(t *testing.T, dir, name string, names []string, issuer *testCert, ca bool) *testCert {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("rand.Int() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ca {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
		template.ExtKeyUsage = nil
	}

	parent, parentKey := template, key
	if issuer != nil {
		parent, parentKey = issuer.cert, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}

	tc := &testCert{
		cert:     cert,
		key:      key,
		certPath: filepath.Join(dir, name+".crt"),
		keyPath:  filepath.Join(dir, name+".key"),
	}
	if err := os.WriteFile(tc.certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing certificate error = %v", err)
	}
	if err := os.WriteFile(tc.keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("writing key error = %v", err)
	}

	return tc
}

// runSpog runs a receiver with args until the test ends, waiting until it
// is serving, checked with TLS for its certificate if cert is not nil
func runSpog(t *testing.T, cert *testCert, args ...string) {
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

	var tlsConf *tls.Config
	if cert != nil {
		var err error
		if tlsConf, err = tlsConfig(cert.certPath); err != nil {
			t.Fatalf("tlsConfig() error = %v", err)
		}
	}

	address := args[len(args)-1]
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := checkHealth(context.Background(), address, tlsConf)
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
	dir := t.TempDir()
	selfSigned := writeCert(t, dir, "self-signed", []string{"receiver.example.com"}, nil, false)
	other := writeCert(t, dir, "other", []string{"receiver.example.com"}, nil, false)
	ca := writeCert(t, dir, "ca", nil, nil, true)
	issued := writeCert(t, dir, "issued", []string{"receiver.example.com"}, ca, false)

	tests := []struct {
		name    string
		setup   func(t *testing.T) []string
		wantErr string
	}{
		{"serving", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, nil, "--address", addr)
			return []string{"--address", addr}
		}, ""},
		{"from environment variables", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, nil, "--address", addr)
			_, port, _ := net.SplitHostPort(addr)

			// as in the container image, which also sets other variables
			t.Setenv("ONMS_GRPC_ADDRESS", ":"+port)
			t.Setenv("ONMS_GRPC_METRICS_ADDRESS", ":0")
			return nil
		}, ""},
		{"tls with a self-signed certificate", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, selfSigned, "--cert", selfSigned.certPath, "--key", selfSigned.keyPath, "--address", addr)
			return []string{"--address", addr, "--cert", selfSigned.certPath}
		}, ""},
		{"tls with a certificate issued by a ca", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, issued, "--cert", issued.certPath, "--key", issued.keyPath, "--address", addr)
			return []string{"--address", addr, "--cert", issued.certPath}
		}, ""},
		{"tls presenting another certificate", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, selfSigned, "--cert", selfSigned.certPath, "--key", selfSigned.keyPath, "--address", addr)
			return []string{"--address", addr, "--cert", other.certPath, "--timeout", "500ms"}
		}, "certificate signed by unknown authority"},
		{"tls without cert", func(t *testing.T) []string {
			addr := freeAddress(t)
			runSpog(t, selfSigned, "--cert", selfSigned.certPath, "--key", selfSigned.keyPath, "--address", addr)
			return []string{"--address", addr, "--timeout", "500ms"}
		}, "checking health of"},
		{"tls with a certificate without names", func(t *testing.T) []string {
			return []string{"--address", freeAddress(t), "--cert", ca.certPath}
		}, "has no DNS names or IP addresses"},
		{"tls with a missing certificate", func(t *testing.T) []string {
			return []string{"--address", freeAddress(t), "--cert", filepath.Join(dir, "missing.crt")}
		}, "reading certificate"},
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

// TestHealthcheckSharesSpogEnvironment checks healthcheck is configured by
// the same environment variables as spog, as in the container image, so
// renaming a shared flag in only one of them is caught
func TestHealthcheckSharesSpogEnvironment(t *testing.T) {
	cert := writeCert(t, t.TempDir(), "receiver", []string{"receiver.example.com"}, nil, false)

	tests := []struct {
		name string
		env  map[string]string
		cert *testCert
	}{
		{"plaintext", map[string]string{}, nil},
		{"tls", map[string]string{"ONMS_GRPC_CERT": cert.certPath, "ONMS_GRPC_KEY": cert.keyPath}, cert},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := freeAddress(t)
			t.Setenv("ONMS_GRPC_ADDRESS", addr)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			// the receiver is configured by the environment alone
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- Execute(ctx, []string{"spog", "--silent"})
			}()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("receiver error = %v", err)
				}
			})

			var tlsConf *tls.Config
			if tt.cert != nil {
				var err error
				if tlsConf, err = tlsConfig(tt.cert.certPath); err != nil {
					t.Fatalf("tlsConfig() error = %v", err)
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if status, _ := checkHealth(context.Background(), addr, tlsConf); status == healthpb.HealthCheckResponse_SERVING {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("receiver configured by the environment is not serving on %s", addr)
				}
				time.Sleep(10 * time.Millisecond)
			}

			// and so is the check, with no arguments
			if err := Execute(context.Background(), []string{"healthcheck", "--timeout", "1s"}); err != nil {
				t.Errorf("Execute() error = %v, want the receiver configured by the same environment checked", err)
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
