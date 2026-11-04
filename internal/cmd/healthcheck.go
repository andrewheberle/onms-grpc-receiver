package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/andrewheberle/simplecommand/koanfcommand"
	"github.com/bep/simplecobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// healthcheckCommand checks whether a receiver is ready, using the gRPC
// health service on its gRPC listener, for the HEALTHCHECK of the container
// image. Its flags are read from the same environment variables as those of
// spog, so it checks the receiver the container runs without any arguments.
type healthcheckCommand struct {
	address string
	cert    string
	timeout time.Duration

	*koanfcommand.Command
}

func (c *healthcheckCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}

	cmd := cd.CobraCommand
	cmd.Flags().StringVar(&c.address, flagAddress, defaultAddress, "Service gRPC listen address of the receiver to check")
	cmd.Flags().StringVar(&c.cert, flagCert, "", "TLS Certificate of the receiver to check, which is connected to with TLS and must present it when set")
	cmd.Flags().DurationVar(&c.timeout, "timeout", 3*time.Second, "How long the check may take")

	return nil
}

func (c *healthcheckCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	target := dialAddress(c.address)

	var tlsConf *tls.Config
	if c.cert != "" {
		var err error
		if tlsConf, err = tlsConfig(c.cert); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status, err := checkHealth(ctx, target, tlsConf)
	if err != nil {
		return fmt.Errorf("checking health of %s: %w", target, err)
	}
	if status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("receiver at %s is %s", target, status)
	}

	_, _ = fmt.Fprintln(cd.CobraCommand.OutOrStdout(), status)

	return nil
}

// tlsConfig returns the TLS configuration to check a receiver that serves
// the certificate in certFile, the first in the file. The receiver is
// checked over loopback, where the address dialled cannot match the names in
// its certificate, so instead it must present that certificate, which is
// trusted on its own and verified for the first name in it. This works
// whether the certificate is self-signed or issued by a CA, as a certificate
// that is itself trusted needs no chain.
func tlsConfig(certFile string) (*tls.Config, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("reading certificate: %w", err)
	}

	var cert *x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type == "CERTIFICATE" {
			if cert, err = x509.ParseCertificate(block.Bytes); err != nil {
				return nil, fmt.Errorf("parsing certificate in %s: %w", certFile, err)
			}
			break
		}
	}
	if cert == nil {
		return nil, fmt.Errorf("no certificate found in %s", certFile)
	}

	var name string
	switch {
	case len(cert.DNSNames) > 0:
		name = cert.DNSNames[0]
	case len(cert.IPAddresses) > 0:
		name = cert.IPAddresses[0].String()
	default:
		return nil, fmt.Errorf("certificate in %s has no DNS names or IP addresses to verify it for", certFile)
	}

	roots := x509.NewCertPool()
	roots.AddCert(cert)

	return &tls.Config{
		RootCAs:    roots,
		ServerName: name,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// checkHealth returns the overall status reported by the gRPC health service
// at target, connecting with tlsConf if it is not nil
func checkHealth(ctx context.Context, target string, tlsConf *tls.Config) (healthpb.HealthCheckResponse_ServingStatus, error) {
	creds := insecure.NewCredentials()
	if tlsConf != nil {
		creds = credentials.NewTLS(tlsConf)
	}

	conn, err := grpc.NewClient("passthrough:///"+target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return healthpb.HealthCheckResponse_UNKNOWN, err
	}
	defer func() { _ = conn.Close() }()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return healthpb.HealthCheckResponse_UNKNOWN, err
	}

	return resp.GetStatus(), nil
}

// dialAddress returns the address to dial to reach a receiver listening on
// address. A receiver listening on every address, such as ":8080" or
// "0.0.0.0:8080", is reached on localhost, which is where the check runs.
func dialAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		// dialling reports the problem with the address
		return address
	}

	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}

	return net.JoinHostPort(host, port)
}
