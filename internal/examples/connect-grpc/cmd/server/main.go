// Copyright 2024 Buf Technologies, Inc.
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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"buf.build/gen/go/connectrpc/eliza/grpc/go/connectrpc/eliza/v1/elizav1grpc"
	elizav1 "buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/vanguard/vanguardgrpc"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

func main() {
	// Setup the gRPC server
	server := grpc.NewServer()
	elizav1grpc.RegisterElizaServiceServer(server, elizaImpl{})

	// Now wrap it with a Vanguard transcoder to upgrade it to
	// also supporting Connect and gRPC-Web (and even REST,
	// if the gRPC service schemas have HTTP annotations).
	handler, err := vanguardgrpc.NewTranscoder(server)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:18181")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// We use the h2c package in order to support HTTP/2 without TLS,
	// so we can handle gRPC requests, which requires HTTP/2, in
	// addition to Connect and gRPC-Web (which work with HTTP 1.1).
	err = http.Serve(listener, h2c.NewHandler(handler, &http2.Server{}))
	if !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type elizaImpl struct {
	elizav1grpc.UnimplementedElizaServiceServer
}

func (e elizaImpl) Say(_ context.Context, _ *elizav1.SayRequest) (*elizav1.SayResponse, error) {
	// Our example therapist isn't very sophisticated.
	return &elizav1.SayResponse{Sentence: "Tell me more about that."}, nil
}

func (e elizaImpl) Converse(server elizav1grpc.ElizaService_ConverseServer) error {
	for {
		_, err := server.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err := server.Send(&elizav1.ConverseResponse{
			Sentence: "Fascinating. Tell me more.",
		}); err != nil {
			return err
		}
	}
}

func (e elizaImpl) Introduce(_ *elizav1.IntroduceRequest, server elizav1grpc.ElizaService_IntroduceServer) error {
	if err := server.Send(&elizav1.IntroduceResponse{
		Sentence: "Hello",
	}); err != nil {
		return err
	}
	return server.Send(&elizav1.IntroduceResponse{
		Sentence: "How are you today?",
	})
}
