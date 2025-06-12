package main

import (
	"io"
	"log"
	"log/slog"
	"net"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type serviceSyncServer struct {
	pb.UnimplementedNmsInventoryServiceSyncServer
}

func (s *serviceSyncServer) AlarmUpdate(stream grpc.BidiStreamingServer[pb.AlarmUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// print message
		slog.Info("AlarmUpdate",
			"id", in.GetInstanceId(),
			"name", in.GetInstanceName(),
			"snapshot", in.GetSnapshot(),
			"alarmcount", len(in.GetAlarms()),
		)
	}
}

func (s *serviceSyncServer) EventUpdate(stream grpc.BidiStreamingServer[pb.EventUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// print message
		slog.Info("EventUpdate",
			"id", in.GetInstanceId(),
			"name", in.GetInstanceName(),
			"snapshot", in.GetSnapshot(),
			"alarmcount", len(in.GetEvent()),
		)
	}
}

func (s *serviceSyncServer) HeartBeatUpdate(stream grpc.BidiStreamingServer[pb.HeartBeat, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// print message
		slog.Info("HeartBeatUpdate",
			"message", in.GetMessage(),
			"instance", in.GetMonitoringInstance(),
			"timestamp", in.GetTimestamp(),
		)
	}
}

func (s *serviceSyncServer) InventoryUpdate(stream grpc.BidiStreamingServer[pb.NmsInventoryUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// print message
		slog.Info("InventoryUpdate",
			"id", in.GetInstanceId(),
			"name", in.GetInstanceName(),
			"snapshot", in.GetSnapshot(),
			"nodecount", len(in.GetNodes()),
		)
	}
}

func main() {
	// command line flags
	pflag.String("address", "localhost:8080", "Service listen address")

	// parse flags
	pflag.Parse()

	// viper setup
	viper.SetEnvPrefix("onms_grpc")
	viper.AutomaticEnv()
	viper.BindPFlags(pflag.CommandLine)

	// set up listener
	l, err := net.Listen("tcp", viper.GetString("address"))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	var opts []grpc.ServerOption

	// create server
	grpcServer := grpc.NewServer(opts...)
	srv := &serviceSyncServer{}

	// register and start server
	pb.RegisterNmsInventoryServiceSyncServer(grpcServer, srv)
	grpcServer.Serve(l)
}
