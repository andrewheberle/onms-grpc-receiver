package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
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
	cmd.Flags().StringVar(&c.address, "address", defaultAddress, "Service gRPC listen address of the receiver to check")
	cmd.Flags().StringVar(&c.cert, "cert", "", "TLS Certificate of the receiver to check, which is connected to with TLS when set")
	cmd.Flags().DurationVar(&c.timeout, "timeout", 3*time.Second, "How long the check may take")

	return nil
}

func (c *healthcheckCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	target := dialAddress(c.address)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status, err := checkHealth(ctx, target, c.cert != "")
	if err != nil {
		return fmt.Errorf("checking health of %s: %w", target, err)
	}
	if status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("receiver at %s is %s", target, status)
	}

	_, _ = fmt.Fprintln(cd.CobraCommand.OutOrStdout(), status)

	return nil
}

// checkHealth returns the overall status reported by the gRPC health service
// at target, connecting with TLS if useTLS is set
func checkHealth(ctx context.Context, target string, useTLS bool) (healthpb.HealthCheckResponse_ServingStatus, error) {
	creds := insecure.NewCredentials()
	if useTLS {
		// The receiver is checked over loopback, where the names in its
		// certificate cannot match the address dialled, and only its health
		// is asked for, so its certificate is not verified.
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // checking our own health over loopback, see above
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
