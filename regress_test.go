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

package vanguard_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"buf.build/gen/go/connectrpc/eliza/connectrpc/go/connectrpc/eliza/v1/elizav1connect"
	elizav1 "buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/connect"
	"connectrpc.com/vanguard"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Reproducer test case provided in https://github.com/connectrpc/vanguard-go/issues/148
func TestIssue148(t *testing.T) {
	t.Parallel()

	// GRPC-web server (process 1)
	mux := http.NewServeMux()
	mux.Handle(elizav1connect.NewElizaServiceHandler(&handler{t: t}))
	grpcwebServer := httptest.NewUnstartedServer(
		h2c.NewHandler(mux, &http2.Server{}),
	)
	grpcwebServer.EnableHTTP2 = true
	grpcwebServer.Start()
	t.Cleanup(grpcwebServer.Close)

	// Vanguard based proxy (process 2)
	webURL, err := url.Parse(grpcwebServer.URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(webURL)
	proxy.FlushInterval = -1 // shouldn't be necessary
	schema, err := protoregistry.GlobalFiles.FindDescriptorByName("connectrpc.eliza.v1.ElizaService")
	require.NoError(t, err)
	serviceDesc, ok := schema.(protoreflect.ServiceDescriptor)
	require.True(t, ok)
	transcoder, err := vanguard.NewTranscoder(
		[]*vanguard.Service{vanguard.NewServiceWithSchema(serviceDesc, proxy)},
		vanguard.WithDefaultServiceOptions(
			vanguard.WithTargetProtocols(vanguard.ProtocolGRPCWeb),
			vanguard.WithTargetCodecs(vanguard.CodecProto),
		),
		vanguard.WithUnknownHandler(proxy),
	)
	require.NoError(t, err)
	vanguardServer := httptest.NewUnstartedServer(
		h2c.NewHandler(
			transcoder,
			&http2.Server{},
		),
	)
	vanguardServer.EnableHTTP2 = true
	vanguardServer.Start()
	t.Cleanup(vanguardServer.Close)

	clientCases := map[string][]connect.ClientOption{
		"proto": {connect.WithGRPC()},
		"json":  {connect.WithGRPC(), connect.WithProtoJSON()},
	}
	for caseName := range clientCases {
		caseName := caseName
		clientOpts := clientCases[caseName]
		t.Run(caseName, func(t *testing.T) {
			t.Parallel()

			// grpc client using h2c (process 3)
			h2cClient := &http.Client{
				Transport: &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					},
				},
			}

			client := elizav1connect.NewElizaServiceClient(h2cClient, vanguardServer.URL, clientOpts...)
			unaryResp, err := client.Say(context.Background(), connect.NewRequest(&elizav1.SayRequest{Sentence: "foo"}))
			require.NoError(t, err)
			require.Equal(t, "echo foo", unaryResp.Msg.Sentence)

			serverStream, err := client.Introduce(context.Background(), connect.NewRequest(&elizav1.IntroduceRequest{Name: "foo"}))
			require.NoError(t, err)
			var responses []string
			for serverStream.Receive() {
				responses = append(responses, serverStream.Msg().Sentence)
			}
			require.NoError(t, serverStream.Err())
			require.Equal(t, []string{"hi", "hi again"}, responses)
		})
	}
}

type handler struct {
	elizav1connect.UnimplementedElizaServiceHandler
	t testing.TB
}

func (h *handler) Say(_ context.Context, req *connect.Request[elizav1.SayRequest]) (*connect.Response[elizav1.SayResponse], error) {
	if req.Peer().Protocol != connect.ProtocolGRPCWeb {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("only accepts grpc-web, got %q", req.Peer().Protocol))
	}
	return connect.NewResponse(&elizav1.SayResponse{Sentence: "echo " + req.Msg.Sentence}), nil
}

func (h *handler) Introduce(_ context.Context, req *connect.Request[elizav1.IntroduceRequest], resp *connect.ServerStream[elizav1.IntroduceResponse]) error {
	if req.Peer().Protocol != connect.ProtocolGRPCWeb {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("only accepts grpc-web, got %q", req.Peer().Protocol))
	}
	_ = resp.Send(&elizav1.IntroduceResponse{Sentence: "hi"})
	_ = resp.Send(&elizav1.IntroduceResponse{Sentence: "hi again"})
	return nil
}
