package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"

	"github.com/andrewheberle/onms-grpc-receiver/pkg/server"
	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/cloudflare/certinel/fswatcher"
	"github.com/oklog/run"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type spogCommand struct {
	logger *slog.Logger

	srv  *server.ServiceSyncServer
	opts []grpc.ServerOption

	cert               string
	key                string
	listenAddress      string
	alertManager       string
	alertManagerScheme string
	alertManagerSrv    string
	urlMapping         map[string]string

	debug  bool
	silent bool

	headers map[string]string

	*simplecommand.Command
}

func (c *spogCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}

	cmd := cd.CobraCommand
	cmd.Flags().StringVar(&c.cert, "cert", "", "TLS Certificate")
	cmd.Flags().StringVar(&c.key, "key", "", "TLS Key")
	cmd.MarkFlagsRequiredTogether("cert", "key")
	cmd.Flags().StringVar(&c.listenAddress, "address", "localhost:8080", "Service listen address")
	cmd.Flags().StringVar(&c.alertManager, "alertmanager.url", "", "Alertmanager URL")
	cmd.Flags().StringVar(&c.alertManagerScheme, "alertmanager.scheme", "http", "Alertmanager scheme (http/https) when SRV records are used")
	cmd.Flags().StringVar(&c.alertManagerSrv, "alertmanager.srv", "", "Alertmanager SRV Record")
	cmd.MarkFlagsMutuallyExclusive("alertmanager.url", "alertmanager.srv")
	cmd.Flags().StringToStringVar(&c.headers, "headers", map[string]string{}, "Custom headers")
	cmd.Flags().StringToStringVar(&c.urlMapping, "map.url", map[string]string{}, "Map instance ID's to URLs")

	cmd.Flags().BoolVar(&c.debug, "debug", false, "Enable debug logging")
	cmd.Flags().BoolVar(&c.silent, "silent", false, "Disable all logging")

	return nil
}

func (c *spogCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	if err := c.Command.PreRun(this, runner); err != nil {
		return err
	}

	// set up logger
	logLevel := new(slog.LevelVar)
	if c.silent {
		c.logger = slog.New(slog.DiscardHandler)
	} else {
		c.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	}

	// switch on debug
	if c.debug {
		logLevel.Set(slog.LevelDebug)
	}

	// server options
	opts := []server.ServiceSyncServerOption{
		server.WithLogger(c.logger),
		server.WithURLMapping(c.urlMapping),
	}

	// set up alertmanager via url
	if c.alertManager != "" {
		u, err := url.Parse(c.alertManager)
		if err != nil {
			return err
		}

		c.logger.Debug("set up alertmanager", "url", u.String())

		opts = append(opts, server.WithAlertmanagerUrl(u))
	}

	// set up alertmanager via SRV
	if c.alertManagerSrv != "" {
		c.logger.Debug("set up alertmanager", "scheme", c.alertManagerScheme, "srv", c.alertManagerSrv)

		opts = append(opts, server.WithAlertManagerSrv(c.alertManagerScheme, c.alertManagerSrv))
	}

	// add custom headers if set
	if len(c.headers) > 0 {
		opts = append(opts, server.WithHeaders(c.headers))
	}

	// set up server
	c.srv = server.NewServiceSyncServer(opts...)

	c.logger.Debug("completed PreRun", "command", this.CobraCommand.Name())

	return nil
}

func (c *spogCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	// set up listener
	l, err := net.Listen("tcp", c.listenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	defer l.Close()

	g := run.Group{}

	// set up TLS for gRPC
	if c.cert != "" && c.key != "" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		certinel, err := fswatcher.New(c.cert, c.key)
		if err != nil {
			return fmt.Errorf("cannot set uo fswatcher: %w", err)
		}

		g.Add(func() error {
			c.logger.Info("started certificate watcher", "cert", c.cert, "key", c.key)

			return certinel.Start(ctx)
		}, func(err error) {
			cancel()
		})

		config := &tls.Config{
			GetCertificate: certinel.GetCertificate,
		}

		c.opts = append(c.opts, grpc.Creds(credentials.NewTLS(config)))
	}

	// create register and add server to run group
	grpcServer := grpc.NewServer(c.opts...)
	pb.RegisterNmsInventoryServiceSyncServer(grpcServer, c.srv)
	g.Add(func() error {
		c.logger.Info("started gRPC server", "address", c.listenAddress)

		return grpcServer.Serve(l)
	}, func(err error) {
		l.Close()
	})

	return g.Run()
}
