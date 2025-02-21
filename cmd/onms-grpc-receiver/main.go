package main

import (
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/onmsgrpc"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type serviceSyncServer struct {
	pb.UnimplementedServiceSyncServer
}

func (s *serviceSyncServer) InventoryUpdate(stream grpc.BidiStreamingServer[pb.InventoryUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// convert services to JSON
		services, err := json.Marshal(in.Services)
		if err != nil {
			slog.Error("error marshaling services into JSON", "error", err)
			continue
		}

		// print message
		slog.Info("InventoryUpdate",
			"foreignSource", in.ForeignSource,
			"foreignType", in.ForeignType,
			"snapshot", in.Snapshot,
			"services", services,
		)
	}
}

func (s *serviceSyncServer) StateUpdate(stream grpc.BidiStreamingServer[pb.StateUpdateList, emptypb.Empty]) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// convert updates to JSON
		updates, err := json.Marshal(in.Updates)
		if err != nil {
			slog.Error("error marshaling services into JSON", "error", err)
			continue
		}

		// print message
		slog.Info("StateUpdate",
			"foreignSource", in.ForeignSource,
			"foreignType", in.ForeignType,
			"updates", updates,
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
	pb.RegisterServiceSyncServer(grpcServer, srv)
	grpcServer.Serve(l)
}
