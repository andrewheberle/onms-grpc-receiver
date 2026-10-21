package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

	delivery, err := s.deliveryConfig.build(s.logger)
	if err != nil {
		return nil, err
	}

	// log every alarm when verbose, or when there is nowhere to send them
	logAlarms := s.verbose || s.deliveryConfig.discovery == discoveryNone

	s.pipeline = newAlertPipeline(translator, delivery, s.flushThreshold, s.flushInterval, logAlarms, s.logger)

	// register metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	s.registry.MustRegister(delivery.collectors()...)
	s.registry.MustRegister(s.pipeline.collectors()...)

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

// Start handles received alarms until ctx is cancelled
func (s *ServiceSyncServer) Start(ctx context.Context) error {
	s.pipeline.run(ctx)

	return nil
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
