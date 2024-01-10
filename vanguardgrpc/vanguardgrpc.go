// Copyright 2023-2024 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package vanguardgrpc provides convenience functions to make it easy to
// wrap your [grpc.Server] with a [vanguard.Transcoder], to upgrade it to
// supporting Connect, gRPC-Web, and REST+JSON protocols.
package vanguardgrpc

import (
	"fmt"

	"connectrpc.com/vanguard"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/protobuf/proto"
)

// NewTranscoder returns a Vanguard handler that wraps the given gRPC server. All
// services registered with the server will be supported by the returned handler.
// The Vanguard handler will be configured to transcode incoming requests to the
// gRPC protocol.
//
// The returned handler will allow data in the "proto" codec through, but must
// transcode other codecs to "proto".  If a gRPC Codec has been registered with the
// name "json" (via [encoding.RegisterCodec]) then the Vanguard handler will pass
// JSON requests through unchanged as well.
//
// For maximum efficiency, especially if REST and/or Connect clients are expected,
// a JSON codec should be registered before calling this function. If the server
// program does not already register such a codec, it may do so via the following:
//
//	encoding.RegisterCodec(vanguardgrpc.NewCodec(&vanguard.JSONCodec{
//		// These fields can be used to customize the serialization and
//		// de-serialization behavior. The options presented below are
//		// highly recommended.
//		MarshalOptions: protojson.MarshalOptions{
//			EmitUnpopulated: true,
//		},
//		UnmarshalOptions: protojson.UnmarshalOptions{
//			DiscardUnknown: true,
//		},
//	})
func NewTranscoder(server *grpc.Server, opts ...vanguard.TranscoderOption) (*vanguard.Transcoder, error) {
	codecs := make([]string, 1, 2)
	codecs[0] = vanguard.CodecProto
	if encoding.GetCodec(vanguard.CodecJSON) != nil {
		codecs = append(codecs, vanguard.CodecJSON)
	}
	svcInfo := server.GetServiceInfo()
	services := make([]*vanguard.Service, 0, len(svcInfo))
	for svcName := range svcInfo {
		services = append(services, vanguard.NewService(svcName, server))
	}
	allOptions := make([]vanguard.TranscoderOption, 0, 1+len(opts))
	allOptions = append(allOptions, vanguard.WithDefaultServiceOptions(
		vanguard.WithTargetCodecs(codecs...),
		vanguard.WithTargetProtocols(vanguard.ProtocolGRPC),
	))
	allOptions = append(allOptions, opts...)
	return vanguard.NewTranscoder(services, allOptions...)
}

// NewCodec returns a gRPC [encoding.Codec] that uses the given
// Vanguard Codec as its backing implementation. In particular, this
// can be combined with [vanguard.JSONCodec] to easily create a gRPC
// Codec to support the "json" message format.
func NewCodec(codec vanguard.Codec) encoding.Codec {
	return &grpcCodec{codec: codec}
}

type grpcCodec struct {
	codec vanguard.Codec
}

func (g *grpcCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("value is not a proto.Message: %T", v)
	}
	return g.codec.MarshalAppend(nil, msg)
}

func (g *grpcCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("value is not a proto.Message: %T", v)
	}
	return g.codec.Unmarshal(data, msg)
}

func (g *grpcCodec) Name() string {
	return g.codec.Name()
}
