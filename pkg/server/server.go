package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ServiceSyncServer struct {
	logger   *slog.Logger
	registry *prometheus.Registry
	verbose  bool

	// how alerts are sent to Alertmanager
	deliveryConfig deliveryConfig

	// how alarms and heartbeats are turned into alerts
	translationConfig translationConfig

	// handles alarm updates and heartbeats once received, built from the
	// flush settings
	flushThreshold int
	flushInterval  time.Duration
	pipeline       *alertPipeline

	// how far the server is through its life, and the gRPC health service
	// that reports it while serving
	readinessMu sync.Mutex
	readiness   readiness
	health      *health.Server

	pb.UnimplementedNmsInventoryServiceSyncServer
}

// NewServiceSyncServer returns a ServiceSyncServer configured by opts. The
// error returned joins every problem with the configuration, so they can all
// be fixed at once.
func NewServiceSyncServer(opts ...ServiceSyncServerOption) (*ServiceSyncServer, error) {
	// set up with defaults
	s := defaultServiceSyncServer()

	var problems []error
	for _, o := range opts {
		problems = append(problems, o(s))
	}

	translator, err := newAlertTranslator(s.translationConfig, s.logger)
	problems = append(problems, err)

	// the delivery and pipeline register their own metrics
	delivery, err := s.deliveryConfig.build(s.logger, s.registry)
	problems = append(problems, err)

	// log every alarm when verbose, or when there is nowhere to send them
	logAlarms := s.verbose || !s.deliveryConfig.enabled()

	s.pipeline, err = newAlertPipeline(translator, delivery, s.flushThreshold, s.flushInterval, logAlarms, s.logger, s.registry)
	problems = append(problems, err)

	// register runtime metrics
	problems = append(problems, register(s.registry,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	))

	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	if !s.deliveryConfig.enabled() {
		s.logger.Warn("no Alertmanager configured, alarms are only logged")
	}

	return s, nil
}

func defaultServiceSyncServer() *ServiceSyncServer {
	return &ServiceSyncServer{
		// default to no logging
		logger: slog.New(slog.DiscardHandler),

		// ensure registry is non-nil
		registry: prometheus.NewRegistry(),

		// default based on upstream
		translationConfig: translationConfig{
			resolveTimeout: time.Minute * 5,
		},

		// cache SRV records for 30s by default
		deliveryConfig: deliveryConfig{
			srvCacheTTL: 30 * time.Second,
		},

		// batching
		flushThreshold: 10,
		flushInterval:  20 * time.Second,
	}
}

func (s *ServiceSyncServer) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		Registry: s.registry,
	})
}

// keepaliveParams are how idle connections are checked. Horizon instances
// send no keepalive pings of their own, so a connection to one that has gone
// away without closing it would otherwise be held until TCP gives up, which
// may take hours. Instead, a connection with nothing received for Time is
// pinged, and closed if the ping is not answered within Timeout. Horizon
// instances send a heartbeat every minute, so this only happens when one
// stops. Connections are never closed for their age alone, as Horizon only
// reconnects after 30 seconds, dropping the alarm updates in between.
var keepaliveParams = keepalive.ServerParameters{
	Time:    2 * time.Minute,
	Timeout: 20 * time.Second,
}

// gracefulStopTimeout is how long gRPC streams are given to finish when
// stopping before they are closed
const gracefulStopTimeout = 3 * time.Second

// Serve receives alarm updates and heartbeats on l until ctx is cancelled or
// serving fails, using opts for the gRPC server.
//
// When ctx is cancelled no new streams are accepted, while open streams are
// given up to gracefulStopTimeout to finish. As alarm updates are received
// until then, the alarms held by the alert pipeline are only flushed once
// gRPC has stopped, and Serve returns once they have been sent, or after
// shutdownFlushTimeout.
//
// The standard gRPC health service is also served, reporting the server as
// serving only while gRPC is serving, as HealthHandler does. Once ctx is
// cancelled or serving fails, both report it as not ready straight away.
//
// Idle connections are checked with keepaliveParams, which opts are applied
// after, so they may replace it.
func (s *ServiceSyncServer) Serve(ctx context.Context, l net.Listener, opts ...grpc.ServerOption) error {
	defer s.setReadiness(stopped)

	grpcServer := grpc.NewServer(append([]grpc.ServerOption{grpc.KeepaliveParams(keepaliveParams)}, opts...)...)
	pb.RegisterNmsInventoryServiceSyncServer(grpcServer, s)

	// the health server reports every service as serving until it is told
	// otherwise, so it is set from the readiness before it is served
	healthServer := health.NewServer()
	s.setHealthServer(healthServer)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	// the pipeline is not stopped by ctx, as it must keep running until gRPC
	// has stopped
	pipelineCtx, stopPipeline := context.WithCancel(context.Background())
	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		s.pipeline.run(pipelineCtx)
	}()

	served := make(chan error, 1)
	go func() {
		s.logger.Info("started gRPC receiver", "address", l.Addr().String())

		// if stopping has already started, the server stays stopping
		s.setReadiness(serving)
		served <- grpcServer.Serve(l)
	}()

	var err error
	select {
	case <-ctx.Done():
		s.logger.Info("stopping gRPC receiver")
		s.setReadiness(stopping)
		timer := time.AfterFunc(gracefulStopTimeout, grpcServer.Stop)
		grpcServer.GracefulStop()
		timer.Stop()

		// if stopped before gRPC started serving, Serve reports that it was
		// stopped, which is expected here
		if err = <-served; errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
	case err = <-served:
		s.setReadiness(stopping)
		grpcServer.Stop()
	}

	// every alarm update has now been received, so flush the alarms held
	stopPipeline()
	<-pipelineDone

	return err
}

func (s *ServiceSyncServer) AlarmUpdate(stream grpc.BidiStreamingServer[pb.AlarmUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			s.logger.Warn("error receiving alarm update", "error", err)
			return err
		}

		s.pipeline.alarmUpdate(in, time.Now())
	}
}

// EventUpdate simply accepts and discards any data to avoid errors on the Horizon side
func (s *ServiceSyncServer) EventUpdate(stream grpc.BidiStreamingServer[pb.EventUpdateList, emptypb.Empty]) error {
	return discard(stream)
}

func (s *ServiceSyncServer) HeartBeatUpdate(stream grpc.BidiStreamingServer[pb.HeartBeat, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		s.pipeline.heartbeat(stream.Context(), in, time.Now())
	}
}

// InventoryUpdate simply accepts and discards any data to avoid errors on the Horizon side
func (s *ServiceSyncServer) InventoryUpdate(stream grpc.BidiStreamingServer[pb.NmsInventoryUpdateList, emptypb.Empty]) error {
	return discard(stream)
}

func discard[T any](stream grpc.BidiStreamingServer[T, emptypb.Empty]) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
