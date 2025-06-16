bsm/bsm.pb.go: proto/bsm.proto
	protoc --proto_path=proto --go_out=pkg/bsm --go_opt=default_api_level=API_OPAQUE --go_opt=paths=source_relative --go_opt=Mmonitored-services.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/bsm monitored-services.proto

bsm/bsm_grpc.pb.go: proto/bsm.proto
	protoc --proto_path=proto --go-grpc_out=pkg/bsm --go-grpc_opt=paths=source_relative --go-grpc_opt=Mmonitored-services.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/bsm monitored-services.proto

spog/spog.pb.go: proto/spog.proto
	protoc --proto_path=proto --go_out=pkg/spog --go_opt=default_api_level=API_OPAQUE --go_opt=paths=source_relative --go_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog spog.proto

spog/spog_grpc.pb.go: proto/spog.proto
	protoc --proto_path=proto --go-grpc_out=pkg/spog --go-grpc_opt=paths=source_relative --go-grpc_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog spog.proto

.PHONY: all
all: bsm/bsm.pb.go bsm/bsm_grpc.pb.go spog/spog.pb.go spog/spog_grpc.pb.go

.PHONY: clean
clean:
	-rm -f bsm/bsm.pb.go
	-rm -f bsm/bsm_grpc.pb.go
	-rm -f spog/spog.pb.go
	-rm -f spog/spog_grpc.pb.go
