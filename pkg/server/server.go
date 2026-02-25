package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ServiceSyncServer struct {
	alertmanagers  func() ([]string, error)
	logger         *slog.Logger
	httpClient     *http.Client
	urlMap         map[string]string
	dnsClient      *net.Resolver
	registry       *prometheus.Registry
	verbose        bool
	resolveTimeout time.Duration

	// metrics
	alertmanagerTotal  *prometheus.CounterVec
	alertmanagerErrors *prometheus.CounterVec
	alarmTotal         *prometheus.CounterVec
	alarmCount         *prometheus.GaugeVec
	heartbeatTotal     *prometheus.CounterVec
	alarmQueueDepth    prometheus.Gauge
	alarmDropped       prometheus.Counter

	// batching
	alarmQueue   chan []instanceAlarm
	batchMaxSize int
	batchMaxWait time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	pb.UnimplementedNmsInventoryServiceSyncServer
}

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

	// set up metrics
	s.alertmanagerTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onmsgrpc_alertmanager_total",
		Help: "Total number of messages sent to alertmanager.",
	},
		[]string{"alertmanager"})
	s.alertmanagerErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onmsgrpc_alertmanager_failed_total",
		Help: "Total number of messages that could not be sent to alertmanager.",
	},
		[]string{"alertmanager"})
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
	s.heartbeatTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "onmsgrpc_heartbeat_total",
		Help: "Total number of heartbeat updates seen from a Horizon instance.",
	},
		[]string{"instance_id"})
	s.alarmQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "onmsgrpc_alarm_queue_depth",
		Help: "Current number of alarm batches waiting in the queue.",
	})
	s.alarmDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "onmsgrpc_alarm_dropped_total",
		Help: "Total number of alarms dropped due to the queue being full.",
	})

	// register metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		s.alertmanagerTotal,
		s.alertmanagerErrors,
		s.alarmTotal,
		s.alarmCount,
		s.heartbeatTotal,
		s.alarmQueueDepth,
		s.alarmDropped,
	)

	return s, nil
}

func defaultServiceSyncServer() *ServiceSyncServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &ServiceSyncServer{
		// default to no logging
		logger: slog.New(slog.DiscardHandler),

		// basic http client
		httpClient: &http.Client{
			Timeout: time.Second * 5,
		},

		// set up dns client
		dnsClient: new(net.Resolver),

		// ensure registry is non-nil
		registry: prometheus.NewRegistry(),

		// default based on upstream
		resolveTimeout: time.Minute * 5,

		// batching
		batchMaxSize: 10,
		batchMaxWait: 20 * time.Second,
		alarmQueue:   make(chan []instanceAlarm, 100),

		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *ServiceSyncServer) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		Registry: s.registry,
	})
}

func (s *ServiceSyncServer) Start() error {
	s.batchWorker()

	return nil
}

func (s *ServiceSyncServer) Shutdown() {
	s.cancel()
}

func (s *ServiceSyncServer) AlarmUpdate(stream grpc.BidiStreamingServer[pb.AlarmUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		id := in.GetInstanceId()
		name := in.GetInstanceName()
		alarms := in.GetAlarms()
		isSnapshot := in.GetSnapshot()

		s.alarmTotal.WithLabelValues(id).Inc()

		if isSnapshot {
			s.alarmCount.WithLabelValues(id).Set(float64(len(alarms)))
		}

		s.logger.Info("AlarmUpdate",
			"instance_id", id,
			"name", name,
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
		select {
		case s.alarmQueue <- wrapped:
			s.alarmQueueDepth.Set(float64(len(s.alarmQueue)))
		default:
			alarmcount := len(alarms)
			s.logger.Warn("alarm queue full, dropping batch", "alarmcount", alarmcount, "instance_id", id)
			s.alarmDropped.Add(float64(alarmcount))
		}
	}
}

func (s *ServiceSyncServer) batchWorker() {
	var batch []instanceAlarm
	timer := time.NewTimer(s.batchMaxWait)
	defer timer.Stop()

	for {
		select {
		case <-s.ctx.Done():
			if len(batch) > 0 {
				s.logger.Info("batchWorker: flushing on shutdown", "alarmcount", len(batch))
				s.handleAlarms(batch)
				s.alarmQueueDepth.Set(0)
			}
			return

		case alarms, ok := <-s.alarmQueue:
			if !ok {
				// channel closed, flush remainder
				if len(batch) > 0 {
					s.logger.Info("batchWorker: flushing on close", "alarmcount", len(batch))
					s.handleAlarms(batch)
					s.alarmQueueDepth.Set(0)
				}
				return
			}

			batch = append(batch, alarms...)

			if len(batch) >= s.batchMaxSize {
				s.logger.Info("batchWorker: flushing on size", "alarmcount", len(batch))
				s.handleAlarms(batch)
				batch = nil
				s.alarmQueueDepth.Set(float64(len(s.alarmQueue)))

				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(s.batchMaxWait)
			}

		case <-timer.C:
			if len(batch) > 0 {
				s.logger.Info("batchWorker: flushing on timer", "alarmcount", len(batch))
				s.handleAlarms(batch)
				batch = nil
				s.alarmQueueDepth.Set(float64(len(s.alarmQueue)))
			}
			timer.Reset(s.batchMaxWait)
		}
	}
}

func (s *ServiceSyncServer) handleAlarms(alarms []instanceAlarm) {
	list := make([]*models.PostableAlert, 0)
	for _, ia := range alarms {
		alarm := ia.alarm
		id := ia.instanceID
		name := ia.instanceName
		now := ia.now

		if s.alertmanagers == nil || s.verbose {
			s.logger.Info("AlarmUpdate",
				"alarm_id", alarm.GetId(),
				"uei", alarm.GetUei(),
				slog.Group("NodeCriteria",
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

			// finish here if no alertmanagers are configured
			if s.alertmanagers == nil {
				continue
			}
		}

		// ignore Normal severity alarms
		if alarm.GetSeverity() == uint32(pb.Severity_NORMAL) {
			continue
		}

		firstEventTime := time.UnixMilli(int64(alarm.GetFirstEventTime()))
		lastEventTime := time.UnixMilli(int64(alarm.GetLastEventTime()))

		if alarm.GetSeverity() != uint32(pb.Severity_CLEARED) && lastEventTime.Before(now.Add(-time.Minute*5)) {
			// skip cleared alarms older than 5-minutes
			continue
		}

		// add basics
		labels := map[string]string{
			"alertname":     alarm.GetUei(),
			"alarm_id":      fmt.Sprint(alarm.GetId()),
			"node_id":       fmt.Sprint(alarm.GetNodeCriteria().GetId()),
			"node_name":     alarm.GetNodeCriteria().GetNodeLabel(),
			"instance_id":   id,
			"instance_name": name,
			"severity":      strings.ToLower(pb.Severity_name[int32(alarm.GetSeverity())]),
		}

		// add service if set
		if service := alarm.GetServiceName(); service != "" {
			labels["service"] = service
		}

		// add ip_address if set
		if ip := alarm.GetIpAddress(); ip != "" {
			labels["ip_address"] = ip
		}

		// set site as node location or site if mapping set
		if location := alarm.GetNodeCriteria().GetLocation(); location != "" {
			labels["site"] = location
		}

		if rk := alarm.GetReductionKey(); rk != "" {
			labels["reduction_key"] = rk
		}

		if ck := alarm.GetClearKey(); ck != "" {
			labels["clear_key"] = ck
		}

		alert := models.Alert{
			Labels: labels,
		}

		// add generator URL if mapping set
		if baseUrl := inmap(id, s.urlMap); baseUrl != "" {
			u, err := url.JoinPath(baseUrl, "/alarm/detail.htm")
			if err != nil {
				s.logger.Error("problem creating generatorURL", "error", err)
				continue
			}
			alert.GeneratorURL = strfmt.URI(u + fmt.Sprintf("?id=%d", alarm.GetId()))
		}

		// default start and end time based on first event time and now + 5m
		post := &models.PostableAlert{
			Alert:    alert,
			StartsAt: strfmt.DateTime(firstEventTime),
			EndsAt:   strfmt.DateTime(time.Now().Add(time.Minute * 5)),
		}

		// set ends at for cleared alerts based on last update time
		if alarm.GetSeverity() == uint32(pb.Severity_CLEARED) {
			post.EndsAt = strfmt.DateTime(lastEventTime)
		}

		// add to list
		list = append(list, post)
	}

	// send to alertmanager at the end
	if err := s.send(list); err != nil {
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

		// finish here if alertmanager is not set
		if s.alertmanagers == nil {
			s.logger.Debug("alertmanager not set")
			continue
		}

		// add heartbeat to list
		labels := map[string]string{
			"alertname":     "OpenNMSHeartbeat",
			"instance_id":   id,
			"instance_name": name,
		}

		now := time.Now()

		hb := &models.PostableAlert{
			Alert: models.Alert{
				Labels: labels,
			},
			StartsAt: strfmt.DateTime(now),
			EndsAt:   strfmt.DateTime(now.Add(s.resolveTimeout)),
		}
		s.logger.Debug("adding message to list", "message", hb)

		// send to alertmanager at the end
		if err := s.send([]*models.PostableAlert{hb}); err != nil {
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

func (s *ServiceSyncServer) send(list []*models.PostableAlert) error {
	if len(list) == 0 {
		return nil
	}

	ams, err := s.alertmanagers()
	if err != nil {
		return err
	}

	payload, err := json.Marshal(list)
	if err != nil {
		return err
	}

	logger := s.logger.With("count", len(list))

	var wg sync.WaitGroup
	for _, am := range ams {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			s.alertmanagerTotal.WithLabelValues(url).Inc()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				logger.Warn("error creating request", "url", url, "error", err)
				s.alertmanagerErrors.WithLabelValues(url).Inc()
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := s.httpClient.Do(req)
			if err != nil {
				logger.Warn("error sending to alertmanager", "url", url, "error", err)
				s.alertmanagerErrors.WithLabelValues(url).Inc()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				logger.Warn("bad status code from alertmanager", "url", url, "status", resp.Status)
				s.alertmanagerErrors.WithLabelValues(url).Inc()
				return
			}

			logger.Info("sent to alertmanager", "url", url, "status", resp.Status)
		}(am)
	}
	wg.Wait()

	return nil
}

func inmap(k string, m map[string]string) string {
	if v, ok := m[k]; ok {
		return v
	}

	return ""
}
