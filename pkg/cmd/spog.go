package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

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
	metricsAddress     string
	metricsPath        string
	alertManagers      []string
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
	cmd.Flags().StringVar(&c.listenAddress, "address", "localhost:8080", "Service gRPC listen address")
	cmd.Flags().StringVar(&c.metricsAddress, "metrics.address", "", "Metrics listen address")
	cmd.Flags().StringVar(&c.metricsPath, "metrics.path", "/metrics", "Metrics path")
	cmd.Flags().StringSliceVar(&c.alertManagers, "alertmanager.url", []string{}, "Alertmanager URL")
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
	if len(c.alertManagers) > 0 {
		c.logger.Debug("set up alertmanager", "urls", c.alertManagers)

		opts = append(opts, server.WithAlertmanagerUrl(c.alertManagers))
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
	srv, err := server.NewServiceSyncServer(opts...)
	if err != nil {
		return err
	}
	c.srv = srv

	c.logger.Debug("completed PreRun", "command", this.CobraCommand.Name())

	return nil
}

func (c *spogCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	var tlsConfig *tls.Config

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

		tlsConfig = &tls.Config{
			GetCertificate: certinel.GetCertificate,
		}

		c.opts = append(c.opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	// create register and add server to run group
	grpcServer := grpc.NewServer(c.opts...)
	pb.RegisterNmsInventoryServiceSyncServer(grpcServer, c.srv)
	g.Add(func() error {
		c.logger.Info("started gRPC receiver", "address", c.listenAddress)

		return grpcServer.Serve(l)
	}, func(err error) {
		l.Close()
	})

	// set up metrics
	if c.metricsAddress != "" {
		mux := http.NewServeMux()
		mux.Handle(c.metricsPath, c.srv.MetricsHandler())

		srv := &http.Server{
			Addr:    c.metricsAddress,
			Handler: mux,
		}

		if c.cert != "" && c.key != "" {
			// add TLS config
			srv.TLSConfig = tlsConfig
			g.Add(func() error {
				// run tls server
				return srv.ListenAndServeTLS("", "")
			}, func(err error) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					srv.Shutdown(ctx)
					cancel()
				}()
			})
		} else {
			g.Add(func() error {
				// run non tls server
				return srv.ListenAndServe()
			}, func(err error) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					srv.Shutdown(ctx)
					cancel()
				}()
			})
		}
	}

	return g.Run()
}
