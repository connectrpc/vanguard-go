// Copyright 2023 Buf Technologies, Inc.
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
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

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

	// NB: We use the h2c package in order to support HTTP/2 without TLS,
	//     so we can handle gRPC requests, which requires HTTP/2, in
	//     addition to Connect and gRPC-Web (which work with HTTP 1.1).
	err = http.Serve(listener, h2c.NewHandler(handler, &http2.Server{}))
	if !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type elizaImpl struct {
	elizav1grpc.UnimplementedElizaServiceServer
}

func (e elizaImpl) Say(_ context.Context, request *elizav1.SayRequest) (*elizav1.SayResponse, error) {
	sentence := strings.TrimSpace(strings.ReplaceAll(request.Sentence, "\n", " "))
	if len(sentence) == 0 {
		return &elizav1.SayResponse{Sentence: "Can you say that again? I didn't understand."}, nil
	}
	switch rand.Intn(4) {
	case 0:
		return &elizav1.SayResponse{Sentence: "Fascinating. Tell me more."}, nil
	case 1:
		return &elizav1.SayResponse{Sentence: "And how does that make you feel?"}, nil
	case 2:
		return &elizav1.SayResponse{Sentence: "Ah, so you say. But what would you say if I told you that you were mistaken?"}, nil
	}
	if strings.Contains(sentence, "\"") {
		return &elizav1.SayResponse{Sentence: "Says who?"}, nil
	}
	r, _ := utf8.DecodeLastRuneInString(sentence)
	if !strings.ContainsRune(".!?", r) {
		sentence += "."
	}
	return &elizav1.SayResponse{Sentence: "You say, \"" + sentence + "\" Are you sure about that?"}, nil
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

func (e elizaImpl) Introduce(request *elizav1.IntroduceRequest, server elizav1grpc.ElizaService_IntroduceServer) error {
	name := strings.TrimRight(strings.TrimSpace(strings.ReplaceAll(request.Name, "\n", " ")), ".!?,:")
	if err := server.Send(&elizav1.IntroduceResponse{
		Sentence: "Hello, " + name + "!",
	}); err != nil {
		return err
	}
	if err := server.Send(&elizav1.IntroduceResponse{
		Sentence: "My name is Dr. Eliza.",
	}); err != nil {
		return err
	}
	if err := server.Send(&elizav1.IntroduceResponse{
		Sentence: "Please tell me a little about yourself.",
	}); err != nil {
		return err
	}
	return server.Send(&elizav1.IntroduceResponse{
		Sentence: "And then tell me about your day and how you are feeling.",
	})
}
