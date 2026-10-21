package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/prometheus/alertmanager/api/v2/models"
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

	// log every alarm in full, when verbose or no Alertmanager is configured
	logAlarms bool

	// sends alerts to Alertmanager, built from deliveryConfig
	deliveryConfig deliveryConfig
	delivery       *alertDelivery

	// turns alarms and heartbeats into alerts
	translator *alertTranslator

	// metrics
	alarmTotal        *prometheus.CounterVec
	alarmCount        *prometheus.GaugeVec
	alarmLastSnapshot *prometheus.GaugeVec
	heartbeatTotal    *prometheus.CounterVec

	// batches alarms before they are handled, built from the flush settings
	flushThreshold int
	flushInterval  time.Duration
	batcher        *alarmBatcher

	pb.UnimplementedNmsInventoryServiceSyncServer
}

// alarmQueueSize is the number of alarm updates that may wait to be batched
const alarmQueueSize = 100

type instanceAlarm struct {
	alarm        *pb.Alarm
	now          time.Time
	instanceID   string
	instanceName string
}

func NewServiceSyncServer(opts ...ServiceSyncServerOption) (*ServiceSyncServer, error) {
	// set up with defaults
	s := defaultServiceSyncServer()

	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, fmt.Errorf("error applying option: %w", err)
		}
	}

	if err := s.translator.validate(); err != nil {
		return nil, err
	}

	delivery, err := s.deliveryConfig.build(s.logger)
	if err != nil {
		return nil, err
	}
	s.delivery = delivery

	s.batcher = newAlarmBatcher(s.handleAlarms, alarmQueueSize, s.flushThreshold, s.flushInterval, s.logger)

	// log every alarm when verbose, or when there is nowhere to send them
	s.logAlarms = s.verbose || s.deliveryConfig.discovery == discoveryNone

	// set up metrics
	s.alarmTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onmsgrpc_alarm_total",
		Help: "Total number of alarm updates seen from a Horizon instance.",
	},
		[]string{"instance_id"})
	s.alarmCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "onmsgrpc_alarm_count",
		Help: "Current number of active alarms for a Horizon instance from the last full snapshot of alarms.",
	},
		[]string{"instance_id"})
	s.alarmLastSnapshot = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "onmsgrpc_alarm_last_snapshot_timestamp_seconds",
		Help: "Unix time of the last full alarm snapshot received from a Horizon instance.",
	},
		[]string{"instance_id"})
	s.heartbeatTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onmsgrpc_heartbeat_total",
		Help: "Total number of heartbeat updates seen from a Horizon instance.",
	},
		[]string{"instance_id"})

	// register metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		s.alarmTotal,
		s.alarmCount,
		s.alarmLastSnapshot,
		s.heartbeatTotal,
	)
	s.registry.MustRegister(s.delivery.collectors()...)
	s.registry.MustRegister(s.batcher.collectors()...)

	return s, nil
}

func defaultServiceSyncServer() *ServiceSyncServer {
	return &ServiceSyncServer{
		// default to no logging
		logger: slog.New(slog.DiscardHandler),

		// ensure registry is non-nil
		registry: prometheus.NewRegistry(),

		// default based on upstream
		translator: &alertTranslator{
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
	s.batcher.run(ctx)

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

		id := in.GetInstanceId()
		name := in.GetInstanceName()
		alarms := in.GetAlarms()
		isSnapshot := in.GetSnapshot()

		s.alarmTotal.WithLabelValues(id).Inc()

		if isSnapshot {
			s.alarmCount.WithLabelValues(id).Set(float64(len(alarms)))
			s.alarmLastSnapshot.WithLabelValues(id).SetToCurrentTime()
		}

		s.logger.Info("AlarmUpdate",
			slog.Group("instance",
				"id", id,
				"name", name,
			),
			"snapshot", isSnapshot,
			"alarmcount", len(alarms),
		)

		// wrap alarms with instance info before enqueuing
		wrapped := make([]instanceAlarm, 0, len(alarms))
		for _, alarm := range alarms {
			wrapped = append(wrapped, instanceAlarm{
				alarm:        alarm,
				instanceID:   id,
				instanceName: name,
				now:          time.Now(),
			})
		}

		// enqueue - drop if full (best effort)
		s.batcher.add(wrapped)
	}
}

func (s *ServiceSyncServer) handleAlarms(alarms []instanceAlarm) {
	list := make([]*models.PostableAlert, 0)
	for _, ia := range alarms {
		alarm := ia.alarm
		id := ia.instanceID
		name := ia.instanceName

		if s.logAlarms {
			s.logger.Info("AlarmUpdate",
				"alarm_id", alarm.GetId(),
				"uei", alarm.GetUei(),
				slog.Group("instance",
					"id", id,
					"name", name,
				),
				slog.Group("node_criteria",
					"id", alarm.GetNodeCriteria().GetId(),
					"foreign_source", alarm.GetNodeCriteria().GetForeignSource(),
					"foreign_id", alarm.GetNodeCriteria().GetForeignId(),
					"node_label", alarm.GetNodeCriteria().GetNodeLabel(),
					"location", alarm.GetNodeCriteria().GetLocation(),
				),
				"ip_address", alarm.GetIpAddress(),
				"service_name", alarm.GetServiceName(),
				"reduction_key", alarm.GetReductionKey(),
				"type", alarm.GetType(),
				"count", alarm.GetCount(),
				"severity", alarm.GetSeverity(),
				"first_event_time", alarm.GetFirstEventTime(),
				"description", alarm.GetDescription(),
				"log_message", alarm.GetLogMessage(),
				"ack_user", alarm.GetAckUser(),
				"ack_time", alarm.GetAckTime(),
				"last_event_time", alarm.GetLastEventTime(),
				"if_index", alarm.GetIfIndex(),
				"operator_instructions", alarm.GetOperatorInstructions(),
				"clear_key", alarm.GetClearKey(),
				"managed_object_instance", alarm.GetManagedObjectInstance(),
				"managed_object_type", alarm.GetManagedObjectType(),
				"relatedAlarm_count", len(alarm.GetRelatedAlarm()),
				"last_update_time", alarm.GetLastUpdateTime(),
			)
		}

		alert, err := s.translator.alarm(ia)
		if err != nil {
			s.logger.Error("problem creating alert", "error", err)
			continue
		}

		// add to list unless skipped
		if alert != nil {
			list = append(list, alert)
		}
	}

	// send to alertmanager at the end
	if err := s.delivery.send(list); err != nil {
		s.logger.Error("error during send", "error", err)
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

		id := in.GetMonitoringInstance().GetInstanceId()
		name := in.GetMonitoringInstance().GetInstanceName()

		// increment heartbeat counter
		s.heartbeatTotal.WithLabelValues(id).Inc()

		// print message
		s.logger.Info(in.GetMessage(),
			slog.Group("instance",
				"id", id,
				"name", name,
			),
			"timestamp", in.GetTimestamp(),
		)

		hb := s.translator.heartbeat(name, time.Now())
		s.logger.Debug("adding message to list", "message", hb)

		// send to alertmanager at the end
		if err := s.delivery.send([]*models.PostableAlert{hb}); err != nil {
			s.logger.Error("error during send", "error", err)
		}
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
