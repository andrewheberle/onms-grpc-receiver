package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
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

	// register metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		s.alertmanagerTotal, s.alertmanagerErrors, s.alarmTotal, s.alarmCount, s.heartbeatTotal,
	)

	return s, nil
}

func defaultServiceSyncServer(opts ...ServiceSyncServerOption) *ServiceSyncServer {
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
	}
}

func (s *ServiceSyncServer) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		Registry: s.registry,
	})
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

		// add number of alarms to counter
		s.alarmTotal.WithLabelValues(in.GetInstanceId()).Inc()

		// if this is a snapshot of all alarms, update the total alarm count
		if in.GetSnapshot() {
			s.alarmCount.WithLabelValues(in.GetInstanceId()).Set(float64(len(in.GetAlarms())))
		}

		logger := s.logger.With("instance_id", in.GetInstanceId(),
			"name", in.GetInstanceName(),
			"snapshot", in.GetSnapshot(),
			"alarmcount", len(in.GetAlarms()),
		)

		logger.Info("AlarmUpdate")

		list := make([]*models.PostableAlert, 0)
		for _, alarm := range in.GetAlarms() {
			if s.alertmanagers == nil || s.verbose {
				logger.Info("AlarmUpdate",
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

			if alarm.GetSeverity() != uint32(pb.Severity_CLEARED) && lastEventTime.Before(time.Now().Add(-time.Minute*5)) {
				// skip cleared alarms older than 5-minutes
				continue
			}

			// add basics
			labels := map[string]string{
				"alertname":     alarm.GetUei(),
				"alarm_id":      fmt.Sprint(alarm.GetId()),
				"node_id":       fmt.Sprint(alarm.GetNodeCriteria().GetId()),
				"node_name":     alarm.GetNodeCriteria().GetNodeLabel(),
				"instance_id":   in.GetInstanceId(),
				"instance_name": in.GetInstanceName(),
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
			if baseUrl := inmap(in.GetInstanceId(), s.urlMap); baseUrl != "" {
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

		// increment heartbeat counter
		s.heartbeatTotal.WithLabelValues(in.GetMonitoringInstance().GetInstanceId()).Inc()

		// print message
		s.logger.Info(in.GetMessage(),
			slog.Group("instance",
				"id", in.GetMonitoringInstance().GetInstanceId(),
				"type", in.GetMonitoringInstance().GetInstanceType(),
				"name", in.GetMonitoringInstance().GetInstanceName(),
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
			"instance_id":   in.GetMonitoringInstance().GetInstanceId(),
			"instance_name": in.GetMonitoringInstance().GetInstanceName(),
			"instance_type": in.GetMonitoringInstance().GetInstanceType(),
		}

		hb := &models.PostableAlert{
			Alert: models.Alert{
				Labels: labels,
			},
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
	if len(list) > 0 {
		ams, err := s.alertmanagers()
		if err != nil {
			return err
		}

		payload, err := json.Marshal(list)
		if err != nil {
			return err
		}

		// try our list
		return func() error {
			logger := s.logger.With("count", len(list))
			for _, am := range ams {
				s.alertmanagerTotal.WithLabelValues(am).Inc()

				// create new buffer from JSON payload
				buf := bytes.NewReader(payload)

				// do http POST
				resp, err := s.httpClient.Post(am, "application/json", buf)
				if err != nil {
					logger.Warn("error sending to alertmanager", "url", am, "status", resp.Status, "error", err)
					s.alertmanagerErrors.WithLabelValues(am).Inc()
					continue
				}

				// check status code
				if resp.StatusCode != http.StatusOK {
					logger.Warn("bad status code from alertmanager", "url", am, "status", resp.Status)
					s.alertmanagerErrors.WithLabelValues(am).Inc()
					continue
				}

				logger.Info("sent to alertmanager", "url", am, "status", resp.Status)
			}

			return nil
		}()
	}

	return nil
}

func inmap(k string, m map[string]string) string {
	if v, ok := m[k]; ok {
		return v
	}

	return ""
}
