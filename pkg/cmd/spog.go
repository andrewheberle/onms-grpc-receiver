package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/cloudflare/certinel/fswatcher"
	"github.com/oklog/run"
	"github.com/prometheus/alertmanager/api/v2/models"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"
)

type spogCommand struct {
	logger *slog.Logger

	srv  *spogServiceSyncServer
	opts []grpc.ServerOption

	cert          string
	key           string
	listenAddress string
	alertManager  string

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
	cmd.Flags().StringVar(&c.listenAddress, "address", "localhost:8080", "Service listen address")
	cmd.Flags().StringVar(&c.alertManager, "alertmanager", "", "Alertmanager address")
	cmd.Flags().StringToStringVar(&c.headers, "headers", map[string]string{}, "Custom headers")

	return nil
}

type customTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	newReq := req.Clone(req.Context())
	if newReq.Header == nil {
		newReq.Header = make(http.Header)
	}
	for key, value := range t.Headers {
		newReq.Header.Set(key, value)
	}

	// Use the underlying transport to execute the request
	return t.transport().RoundTrip(newReq)
}

func (t *customTransport) transport() http.RoundTripper {
	if t.Transport != nil {
		return t.Transport
	}
	return http.DefaultTransport
}

func (c *spogCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	if err := c.Command.PreRun(this, runner); err != nil {
		return err
	}

	// set up server
	c.srv = &spogServiceSyncServer{
		client: &http.Client{
			Timeout: time.Second * 5,
		},
		logger: c.logger,
	}

	// set up alert manager url
	if c.alertManager != "" {
		u, err := url.Parse(c.alertManager)
		if err != nil {
			return err
		}

		c.srv.alertmanager = u
	}

	// add custom headers if set
	if len(c.headers) > 0 {
		c.srv.client.Transport = &customTransport{
			Headers: c.headers,
		}

	}

	// set up logger
	logLevel := new(slog.LevelVar)
	silent, err := this.CobraCommand.InheritedFlags().GetBool("silent")
	if err == nil && silent {
		c.logger = slog.New(slog.DiscardHandler)
	} else {
		c.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	}

	// switch on debug
	debug, err := this.CobraCommand.InheritedFlags().GetBool("debug")
	if err == nil && debug {
		logLevel.Set(slog.LevelDebug)
	}

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

type spogServiceSyncServer struct {
	alertmanager *url.URL
	logger       *slog.Logger
	client       *http.Client

	pb.UnimplementedNmsInventoryServiceSyncServer
}

func (s *spogServiceSyncServer) AlarmUpdate(stream grpc.BidiStreamingServer[pb.AlarmUpdateList, emptypb.Empty]) error {
	list := make([]*models.PostableAlert, 0)
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			// send to alertmanager at the end
			return s.send(list)
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

		for _, alarm := range in.GetAlarms() {
			if s.alertmanager == nil {
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
				}

				// add service if set
				if service := alarm.GetServiceName(); service != "" {
					labels["service"] = service
				}

				// add ip_address if set
				if ip := alarm.GetIpAddress(); ip != "" {
					labels["ip_address"] = ip
				}

				// add to list
				list = append(list, &models.PostableAlert{
					Alert: models.Alert{
						Labels: labels,
					},
				})
			}
		}
	}
}

func (s *spogServiceSyncServer) HeartBeatUpdate(stream grpc.BidiStreamingServer[pb.HeartBeat, emptypb.Empty]) error {
	list := make([]*models.PostableAlert, 0)
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			// send to alertmanager at the end
			return s.send(list)
		}
		if err != nil {
			return err
		}

		// print message
		s.logger.Info("HeartBeatUpdate",
			"message", in.GetMessage(),
			"instance", in.GetMonitoringInstance(),
			"timestamp", in.GetTimestamp(),
		)

		// finish here if alertmanager is not set
		if s.alertmanager == nil {
			continue
		}

		// add heartbeat to list
		list = append(list, &models.PostableAlert{
			Alert: models.Alert{
				Labels: map[string]string{
					"alertname":     "OpenNMSHeartbeat",
					"instance_id":   in.GetMonitoringInstance().GetInstanceId(),
					"instance_name": in.GetMonitoringInstance().GetInstanceName(),
					"instance_type": in.GetMonitoringInstance().GetInstanceType(),
				},
			},
		})
	}
}

func (s *spogServiceSyncServer) send(list []*models.PostableAlert) error {
	if len(list) > 0 {
		buf := new(bytes.Buffer)
		enc := json.NewEncoder(buf)
		if err := enc.Encode(&list); err != nil {
			return err
		}

		resp, err := s.client.Post(fmt.Sprintf("%s://%s/api/v2/alerts", s.alertmanager.Scheme, s.alertmanager.Host), "application/json", buf)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("bad status code from alertmanager: %d", resp.StatusCode)
		}
	}

	return nil
}
