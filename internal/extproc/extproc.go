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

package extproc

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"buf.build/gen/go/envoyproxy/envoy/bufbuild/connect-go/envoy/service/ext_proc/v3/ext_procv3connect"
	ext_procv3 "buf.build/gen/go/envoyproxy/envoy/protocolbuffers/go/envoy/service/ext_proc/v3"
	"github.com/bufbuild/connect-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
)

type ExternalProcessor interface {
	Run(_ context.Context) error
}

type externalProcessor struct {
	h2cAddress string
	tlsAddress string
	tlsConfig  *tls.Config
}

var _ ext_procv3connect.ExternalProcessorHandler = (*externalProcessor)(nil)

func NewExternalProcessor(h2cAddress, tlsAddress string, tlsConfig *tls.Config) (ExternalProcessor, error) {
	return &externalProcessor{
		h2cAddress: h2cAddress,
		tlsAddress: tlsAddress,
		tlsConfig:  tlsConfig,
	}, nil
}

func (proc *externalProcessor) Run(ctx context.Context) error {
	_, handler := ext_procv3connect.NewExternalProcessorHandler(proc)
	h2Server := new(http2.Server)
	server := new(http.Server)
	server.Addr = proc.h2cAddress
	server.Handler = h2c.NewHandler(handler, h2Server)
	server.BaseContext = func(net.Listener) context.Context {
		return ctx
	}
	// TODO: timeouts/etc.
	server.ReadHeaderTimeout = 5 * time.Second

	if err := http2.ConfigureServer(server, h2Server); err != nil {
		return err
	}

	group, ctx := errgroup.WithContext(ctx)

	if proc.h2cAddress != "" {
		group.Go(func() error {
			ln, err := net.Listen("tcp", proc.h2cAddress)
			if err != nil {
				return err
			}
			defer ln.Close()
			return server.Serve(ln)
		})
	}

	if proc.tlsAddress != "" && server.TLSConfig != nil {
		group.Go(func() error {
			ln, err := net.Listen("tcp", proc.h2cAddress)
			if err != nil {
				return err
			}
			defer ln.Close()
			tlsListener := tls.NewListener(ln, server.TLSConfig)
			return server.Serve(tlsListener)
		})
	}

	return group.Wait()
}

func (proc *externalProcessor) Process(
	ctx context.Context,
	stream *connect.BidiStream[ext_procv3.ProcessingRequest, ext_procv3.ProcessingResponse],
) error {
	for {
		request, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		var response *ext_procv3.ProcessingResponse
		switch requestType := request.Request.(type) {
		case *ext_procv3.ProcessingRequest_RequestHeaders:
			response, err = proc.processRequestHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseHeaders:
			response, err = proc.processResponseHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestBody:
			response, err = proc.processRequestBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseBody:
			response, err = proc.processResponseBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestTrailers:
			response, err = proc.processRequestTrailers(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseTrailers:
			response, err = proc.processResponseTrailers(ctx, requestType)
		default:
			err = connect.NewError(connect.CodeInvalidArgument, errors.New("unknown request type"))
		}
		if err != nil {
			return err
		}
		err = stream.Send(response)
		if err != nil {
			return err
		}
	}
}

func (proc *externalProcessor) processRequestHeaders(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_RequestHeaders,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (proc *externalProcessor) processResponseHeaders(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseHeaders,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (proc *externalProcessor) processRequestBody(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_RequestBody,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (proc *externalProcessor) processResponseBody(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseBody,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (proc *externalProcessor) processRequestTrailers(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_RequestTrailers,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (proc *externalProcessor) processResponseTrailers(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseTrailers,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
