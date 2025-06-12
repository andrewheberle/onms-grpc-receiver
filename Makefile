spog/spog.pb.go: proto/spog.proto
	protoc --proto_path=proto --go_out=pkg/spog --go_opt=paths=source_relative --go_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog spog.proto

spog/spog_grpc.pb.go: proto/spog.proto
	protoc --proto_path=proto --go-grpc_out=pkg/spog --go-grpc_opt=paths=source_relative --go-grpc_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog spog.proto

.PHONY: all
all: spog/spog.pb.go spog/spog_grpc.pb.go

.PHONY: clean
clean:
	-rm -f spog/spog.pb.go
	-rm -f spog/spog_grpc.pb.go