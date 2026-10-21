package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
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

	pb.UnimplementedNmsInventoryServiceSyncServer
}

func NewServiceSyncServer(opts ...ServiceSyncServerOption) (*ServiceSyncServer, error) {
	// set up with defaults
	s := defaultServiceSyncServer()

	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, fmt.Errorf("error applying option: %w", err)
		}
	}

	translator, err := newAlertTranslator(s.translationConfig, s.logger)
	if err != nil {
		return nil, err
	}

	// the delivery and pipeline register their own metrics
	delivery, err := s.deliveryConfig.build(s.logger, s.registry)
	if err != nil {
		return nil, err
	}

	// log every alarm when verbose, or when there is nowhere to send them
	logAlarms := s.verbose || s.deliveryConfig.discovery == discoveryNone

	s.pipeline = newAlertPipeline(translator, delivery, s.flushThreshold, s.flushInterval, logAlarms, s.logger, s.registry)

	// register runtime metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

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

// gracefulStopTimeout is how long gRPC streams are given to finish when
// stopping before they are closed
const gracefulStopTimeout = 3 * time.Second

// Serve receives alarm updates and heartbeats on l until ctx is cancelled or
// serving fails, using opts for the gRPC server.
//
// When ctx is cancelled no new streams are accepted, while open streams are
// given up to gracefulStopTimeout to finish. As alarm updates are received
// until then, the alarms held by the alert pipeline are only flushed once
// gRPC has stopped, and Serve returns once they have been sent.
func (s *ServiceSyncServer) Serve(ctx context.Context, l net.Listener, opts ...grpc.ServerOption) error {
	grpcServer := grpc.NewServer(opts...)
	pb.RegisterNmsInventoryServiceSyncServer(grpcServer, s)

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
		served <- grpcServer.Serve(l)
	}()

	var err error
	select {
	case <-ctx.Done():
		s.logger.Info("stopping gRPC receiver")
		timer := time.AfterFunc(gracefulStopTimeout, grpcServer.Stop)
		grpcServer.GracefulStop()
		timer.Stop()
		err = <-served
	case err = <-served:
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

		s.pipeline.heartbeat(in, time.Now())
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
