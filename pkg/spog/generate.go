package spog

// The protoc plugins are the versions pinned by the tool directives in
// go.mod, which go tool -n builds if needed and prints the path of.
//go:generate sh -c "protoc --proto_path=../../proto --plugin=protoc-gen-go=$(go tool -n protoc-gen-go) --plugin=protoc-gen-go-grpc=$(go tool -n protoc-gen-go-grpc) --go_out=. --go_opt=paths=source_relative --go_opt=default_api_level=API_OPAQUE --go_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog --go-grpc_out=. --go-grpc_opt=paths=source_relative --go-grpc_opt=Mspog.proto=github.com/andrewheberle/onms-grpc-receiver/pkg/spog spog.proto"
