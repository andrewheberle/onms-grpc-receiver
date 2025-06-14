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
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ServiceSyncServer struct {
	alertmanagers func() ([]string, error)
	logger        *slog.Logger
	httpClient    *http.Client
	urlMap        map[string]string
	dnsClient     *net.Resolver

	pb.UnimplementedNmsInventoryServiceSyncServer
}

func NewServiceSyncServer(opts ...ServiceSyncServerOption) (*ServiceSyncServer, error) {
	s := new(ServiceSyncServer)

	// default to no logging
	s.logger = slog.New(slog.DiscardHandler)

	// basic http client
	s.httpClient = &http.Client{
		Timeout: time.Second * 5,
	}

	// set up dns client
	s.dnsClient = new(net.Resolver)

	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, fmt.Errorf("error applying option: %w", err)
		}
	}

	return s, nil
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

		logger := s.logger.With("id", in.GetInstanceId(),
			"name", in.GetInstanceName(),
			"snapshot", in.GetSnapshot(),
			"alarmcount", len(in.GetAlarms()),
		)

		logger.Info("AlarmUpdate")

		list := make([]*models.PostableAlert, 0)
		for _, alarm := range in.GetAlarms() {
			if s.alertmanagers == nil {
				logger.Info("AlarmUpdate",
					"id", alarm.GetId(),
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

				continue
			}

			if alarm.GetSeverity() != uint32(pb.Severity_CLEARED) {
				// add basics
				labels := map[string]string{
					"alertname":     alarm.GetUei(),
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

				alert := models.Alert{
					Labels: labels,
				}

				// add generator URL if mapping set
				if baseUrl := inmap(in.GetInstanceId(), s.urlMap); baseUrl != "" {
					u, err := url.JoinPath(baseUrl, fmt.Sprintf("/alarm/detail.htm?id=%d", alarm.GetId()))
					if err != nil {
						s.logger.Error("problem creating generatorURL", "error", err)
						continue
					}
					alert.GeneratorURL = strfmt.URI(u)
				}

				// add to list
				list = append(list, &models.PostableAlert{
					Alert: alert,
				})
			}
		}

		// send to alertmanager at the end
		if err := s.send(list); err != nil {
			s.logger.Error("error during send", "error", err)
		}
	}
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

func (s *ServiceSyncServer) send(list []*models.PostableAlert) error {
	if len(list) > 0 {
		buf := new(bytes.Buffer)
		enc := json.NewEncoder(buf)
		if err := enc.Encode(&list); err != nil {
			return err
		}

		ams, err := s.alertmanagers()
		if err != nil {
			return err
		}

		// try our list
		return func() error {
			logger := s.logger.With("count", len(list))
			for _, am := range ams {
				resp, err := s.httpClient.Post(am, "application/json", buf)
				if err != nil {
					logger.Warn("error sending to alertmanager", "url", am, "status", resp.Status, "error", err)
					continue
				}

				if resp.StatusCode != http.StatusOK {
					logger.Warn("bad status code from alertmanager", "url", am, "status", resp.Status)
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
