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

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"

	"buf.build/gen/go/connectrpc/eliza/connectrpc/go/connectrpc/eliza/v1/elizav1connect"
	elizav1 "buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/connect"
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
		fmt.Println("eliza: ", stream.Msg().GetSentence())
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
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("eliza: ", resp.Msg.GetSentence())
		fmt.Println()
	}
}
