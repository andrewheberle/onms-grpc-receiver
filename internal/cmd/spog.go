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
	"github.com/andrewheberle/simplecommand/koanfcommand"
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
	resolveTimeout     time.Duration
	resolveHold        time.Duration
	srvCacheTTL        time.Duration
	maxRecvMsgSize     int

	debug   bool
	silent  bool
	verbose bool

	headers map[string]string

	alertLabels      map[string]string
	alertAnnotations map[string]string
	alertRunbookURL  string
	alertSeverity    map[string]string

	*koanfcommand.Command
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
	cmd.Flags().StringToStringVar(&c.alertLabels, "alert.labels", map[string]string{}, "Labels added to all alerts")
	cmd.Flags().StringToStringVar(&c.alertAnnotations, "alert.annotations", map[string]string{}, "Annotations added to all alerts")
	cmd.Flags().StringVar(&c.alertRunbookURL, "alert.runbook-url", "", "Template for the runbook URL of alerts without one in their operator instructions")
	cmd.Flags().StringToStringVar(&c.alertSeverity, "alert.severity", map[string]string{}, "Map OpenNMS severities to alert severities")
	cmd.Flags().StringToStringVar(&c.urlMapping, "map.url", map[string]string{}, "Map instance ID's to URLs")
	cmd.Flags().DurationVar(&c.resolveTimeout, "resolve.timeout", time.Minute*5, "Resolve timeout for alarms")
	cmd.Flags().DurationVar(&c.resolveHold, "resolve.hold", 0, "Extra time firing alarm alerts stay active after the resolve timeout, to handle OpenNMS restarts")
	cmd.Flags().DurationVar(&c.srvCacheTTL, "srv.ttl", time.Second*30, "TTL for resolved SRV records")
	cmd.Flags().IntVar(&c.maxRecvMsgSize, "grpc.max-recv-size", 64*1024*1024, "Maximum gRPC message size in bytes that can be received")

	cmd.Flags().BoolVar(&c.debug, "debug", false, "Enable debug logging")
	cmd.Flags().BoolVar(&c.silent, "silent", false, "Disable all logging")
	cmd.Flags().BoolVar(&c.verbose, "verbose", false, "Log all messages")

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
		server.WithResolveTimeout(c.resolveTimeout),
		server.WithResolveHold(c.resolveHold),
		server.WithSRVCacheTTL(c.srvCacheTTL),
		server.WithAlertLabels(c.alertLabels),
		server.WithAlertAnnotations(c.alertAnnotations),
		server.WithRunbookURLTemplate(c.alertRunbookURL),
		server.WithSeverityMap(c.alertSeverity),
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

	// enable verbose logging
	if c.verbose {
		opts = append(opts, server.WithVerbose())
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
	defer func() { _ = l.Close() }()

	g := run.Group{}

	// stop when ctx is cancelled, which happens on SIGINT or SIGTERM
	stopCtx, stop := context.WithCancel(ctx)
	defer stop()
	g.Add(func() error {
		<-stopCtx.Done()
		c.logger.Info("shutting down")

		return nil
	}, func(err error) {
		stop()
	})

	// set up TLS for gRPC
	if c.cert != "" && c.key != "" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		certinel, err := fswatcher.New(c.cert, c.key)
		if err != nil {
			return fmt.Errorf("cannot set up fswatcher: %w", err)
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

	// alarm snapshots contain every alarm in a single message, so allow for
	// messages larger than the 4MiB gRPC default
	c.opts = append(c.opts, grpc.MaxRecvMsgSize(c.maxRecvMsgSize))

	// receive alarm updates and heartbeats until interrupted, after which
	// Serve stops gRPC and flushes the alarms it holds before returning
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	g.Add(func() error {
		return c.srv.Serve(serveCtx, l, c.opts...)
	}, func(err error) {
		stopServe()
	})

	// set up metrics
	if c.metricsAddress != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Healthy"))
		})
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
				c.logger.Info("started metrics service", "address", c.metricsAddress, "path", c.metricsPath, "cert", c.cert, "key", c.key)
				return srv.ListenAndServeTLS("", "")
			}, func(err error) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					_ = srv.Shutdown(ctx)
					cancel()
				}()
			})
		} else {
			g.Add(func() error {
				// run non tls server
				c.logger.Info("started metrics service", "address", c.metricsAddress, "path", c.metricsPath)
				return srv.ListenAndServe()
			}, func(err error) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					_ = srv.Shutdown(ctx)
					cancel()
				}()
			})
		}
	}

	return g.Run()
}
