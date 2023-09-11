// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
	"connectrpc.com/vanguard"
	"google.golang.org/grpc"
)

func main() {
	svr := grpc.NewServer()
	elizav1grpc.RegisterElizaServiceServer(svr, elizaImpl{})
	mux := vanguard.Mux{
		Protocols: []vanguard.Protocol{vanguard.ProtocolGRPC},
		Codecs:    []string{vanguard.CodecProto},
	}
	err := mux.RegisterServiceByName(svr, "connectrpc.eliza.v1.ElizaService")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	l, err := net.Listen("tcp", "127.0.0.1:18181")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = http.Serve(l, mux.AsHandler())
	if err != http.ErrServerClosed {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type elizaImpl struct {
	elizav1grpc.UnimplementedElizaServiceServer
}

func (e elizaImpl) Say(_ context.Context, request *elizav1.SayRequest) (*elizav1.SayResponse, error) {
	sentence := strings.TrimSpace(strings.Replace(request.Sentence, "\n", " ", -1))
	var reply string
	if len(sentence) == 0 {
		reply = "Can you say that again? I didn't understand."
	} else {
		switch rand.Intn(4) {
		case 0:
			reply = "Fascinating. Tell me more."
		case 1:
			reply = "And how does that make you feel?"
		case 2:
			reply = "Ah, so you say. But what would you say if I told you that you were mistaken?"
		case 3:
			if strings.Contains(sentence, "\"") {
				reply = "Says who?"
			} else {
				r, _ := utf8.DecodeLastRuneInString(sentence)
				if !strings.ContainsRune(".!?", r) {
					sentence = sentence + "."
				}
				reply = "You say, \"" + sentence + "\" Are you sure about that?"
			}
		}
	}

	return &elizav1.SayResponse{
		Sentence: reply,
	}, nil
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
	name := strings.TrimRight(strings.TrimSpace(strings.Replace(request.Name, "\n", " ", -1)), ".!?,:")
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
	if err := server.Send(&elizav1.IntroduceResponse{
		Sentence: "And then tell me about your day and how you are feeling.",
	}); err != nil {
		return err
	}
	return nil
}
