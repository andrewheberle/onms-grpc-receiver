//
// Licensed to The OpenNMS Group, Inc (TOG) under one or more
// contributor license agreements.  See the LICENSE.md file
// distributed with this work for additional information
// regarding copyright ownership.
//
// TOG licenses this file to You under the GNU Affero General
// Public License Version 3 (the "License") or (at your option)
// any later version.  You may not use this file except in
// compliance with the License.  You may obtain a copy of the
// License at:
//
//      https://www.gnu.org/licenses/agpl-3.0.txt
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied.  See the License for the specific
// language governing permissions and limitations under the
// License.

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v6.31.1
// source: monitored-services.proto

package bsm

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	ServiceSync_InventoryUpdate_FullMethodName = "/org.opennms.plugin.grpc.proto.services.ServiceSync/InventoryUpdate"
	ServiceSync_StateUpdate_FullMethodName     = "/org.opennms.plugin.grpc.proto.services.ServiceSync/StateUpdate"
	ServiceSync_HeartBeatUpdate_FullMethodName = "/org.opennms.plugin.grpc.proto.services.ServiceSync/HeartBeatUpdate"
)

// ServiceSyncClient is the client API for ServiceSync service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ServiceSyncClient interface {
	InventoryUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[InventoryUpdateList, emptypb.Empty], error)
	StateUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[StateUpdateList, emptypb.Empty], error)
	HeartBeatUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[HeartBeat, emptypb.Empty], error)
}

type serviceSyncClient struct {
	cc grpc.ClientConnInterface
}

func NewServiceSyncClient(cc grpc.ClientConnInterface) ServiceSyncClient {
	return &serviceSyncClient{cc}
}

func (c *serviceSyncClient) InventoryUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[InventoryUpdateList, emptypb.Empty], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &ServiceSync_ServiceDesc.Streams[0], ServiceSync_InventoryUpdate_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[InventoryUpdateList, emptypb.Empty]{ClientStream: stream}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_InventoryUpdateClient = grpc.BidiStreamingClient[InventoryUpdateList, emptypb.Empty]

func (c *serviceSyncClient) StateUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[StateUpdateList, emptypb.Empty], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &ServiceSync_ServiceDesc.Streams[1], ServiceSync_StateUpdate_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[StateUpdateList, emptypb.Empty]{ClientStream: stream}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_StateUpdateClient = grpc.BidiStreamingClient[StateUpdateList, emptypb.Empty]

func (c *serviceSyncClient) HeartBeatUpdate(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[HeartBeat, emptypb.Empty], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &ServiceSync_ServiceDesc.Streams[2], ServiceSync_HeartBeatUpdate_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[HeartBeat, emptypb.Empty]{ClientStream: stream}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_HeartBeatUpdateClient = grpc.BidiStreamingClient[HeartBeat, emptypb.Empty]

// ServiceSyncServer is the server API for ServiceSync service.
// All implementations must embed UnimplementedServiceSyncServer
// for forward compatibility.
type ServiceSyncServer interface {
	InventoryUpdate(grpc.BidiStreamingServer[InventoryUpdateList, emptypb.Empty]) error
	StateUpdate(grpc.BidiStreamingServer[StateUpdateList, emptypb.Empty]) error
	HeartBeatUpdate(grpc.BidiStreamingServer[HeartBeat, emptypb.Empty]) error
	mustEmbedUnimplementedServiceSyncServer()
}

// UnimplementedServiceSyncServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedServiceSyncServer struct{}

func (UnimplementedServiceSyncServer) InventoryUpdate(grpc.BidiStreamingServer[InventoryUpdateList, emptypb.Empty]) error {
	return status.Errorf(codes.Unimplemented, "method InventoryUpdate not implemented")
}
func (UnimplementedServiceSyncServer) StateUpdate(grpc.BidiStreamingServer[StateUpdateList, emptypb.Empty]) error {
	return status.Errorf(codes.Unimplemented, "method StateUpdate not implemented")
}
func (UnimplementedServiceSyncServer) HeartBeatUpdate(grpc.BidiStreamingServer[HeartBeat, emptypb.Empty]) error {
	return status.Errorf(codes.Unimplemented, "method HeartBeatUpdate not implemented")
}
func (UnimplementedServiceSyncServer) mustEmbedUnimplementedServiceSyncServer() {}
func (UnimplementedServiceSyncServer) testEmbeddedByValue()                     {}

// UnsafeServiceSyncServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ServiceSyncServer will
// result in compilation errors.
type UnsafeServiceSyncServer interface {
	mustEmbedUnimplementedServiceSyncServer()
}

func RegisterServiceSyncServer(s grpc.ServiceRegistrar, srv ServiceSyncServer) {
	// If the following call pancis, it indicates UnimplementedServiceSyncServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&ServiceSync_ServiceDesc, srv)
}

func _ServiceSync_InventoryUpdate_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(ServiceSyncServer).InventoryUpdate(&grpc.GenericServerStream[InventoryUpdateList, emptypb.Empty]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_InventoryUpdateServer = grpc.BidiStreamingServer[InventoryUpdateList, emptypb.Empty]

func _ServiceSync_StateUpdate_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(ServiceSyncServer).StateUpdate(&grpc.GenericServerStream[StateUpdateList, emptypb.Empty]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_StateUpdateServer = grpc.BidiStreamingServer[StateUpdateList, emptypb.Empty]

func _ServiceSync_HeartBeatUpdate_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(ServiceSyncServer).HeartBeatUpdate(&grpc.GenericServerStream[HeartBeat, emptypb.Empty]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type ServiceSync_HeartBeatUpdateServer = grpc.BidiStreamingServer[HeartBeat, emptypb.Empty]

// ServiceSync_ServiceDesc is the grpc.ServiceDesc for ServiceSync service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ServiceSync_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "org.opennms.plugin.grpc.proto.services.ServiceSync",
	HandlerType: (*ServiceSyncServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "InventoryUpdate",
			Handler:       _ServiceSync_InventoryUpdate_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "StateUpdate",
			Handler:       _ServiceSync_StateUpdate_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "HeartBeatUpdate",
			Handler:       _ServiceSync_HeartBeatUpdate_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "monitored-services.proto",
}
