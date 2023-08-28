// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"

	"connectrpc.com/connect"
	elizav1 "github.com/bufbuild/vanguard-go/tmp/gen/connectrpc/eliza/v1"
	"github.com/bufbuild/vanguard-go/tmp/gen/connectrpc/eliza/v1/elizav1connect"
)

func main() {
	client := elizav1connect.NewElizaServiceClient(http.DefaultClient, "http://127.0.0.1:18181/",
		connect.WithHTTPGet(),
		connect.WithProtoJSON())

	fmt.Print("What is your name? ")
	input := bufio.NewReader(os.Stdin)
	str, err := input.ReadString('\n')
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stream, err := client.Introduce(context.Background(), connect.NewRequest(&elizav1.IntroduceRequest{
		Name: str,
	}))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for stream.Receive() {
		fmt.Println("eliza: ", stream.Msg().Sentence)
	}
	if err := stream.Err(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println()

	for {
		fmt.Print("you: ")
		input := bufio.NewReader(os.Stdin)
		str, err := input.ReadString('\n')
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		resp, err := client.Say(context.Background(), connect.NewRequest(&elizav1.SayRequest{Sentence: str}))
		if err := stream.Err(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("eliza: ", resp.Msg.Sentence)
		fmt.Println()
	}
}
