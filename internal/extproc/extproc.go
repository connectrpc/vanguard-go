// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
	corev3 "buf.build/gen/go/envoyproxy/envoy/protocolbuffers/go/envoy/config/core/v3"
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
	h2Server := &http2.Server{}
	server := &http.Server{
		Addr:    proc.h2cAddress,
		Handler: h2c.NewHandler(handler, h2Server),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		ReadHeaderTimeout: 5 * time.Second,
	}

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
		response := &ext_procv3.ProcessingResponse{}
		switch requestType := request.Request.(type) {
		case *ext_procv3.ProcessingRequest_RequestHeaders:
			response.Response, err = proc.processRequestHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseHeaders:
			response.Response, err = proc.processResponseHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestBody:
			response.Response, err = proc.processRequestBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseBody:
			response.Response, err = proc.processResponseBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestTrailers:
			response.Response, err = proc.processRequestTrailers(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseTrailers:
			response.Response, err = proc.processResponseTrailers(ctx, requestType)
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
) (*ext_procv3.ProcessingResponse_RequestHeaders, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_RequestHeaders{
		RequestHeaders: &ext_procv3.HeadersResponse{
			Response: &ext_procv3.CommonResponse{
				Status: ext_procv3.CommonResponse_CONTINUE,
			},
		},
	}, nil
}

func (proc *externalProcessor) processResponseHeaders(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseHeaders,
) (*ext_procv3.ProcessingResponse_ResponseHeaders, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &ext_procv3.HeadersResponse{
			Response: &ext_procv3.CommonResponse{
				Status: ext_procv3.CommonResponse_CONTINUE,
				HeaderMutation: &ext_procv3.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{
						{
							Header: &corev3.HeaderValue{
								Key:   "Ext-Proc-Test",
								Value: "This header was set using an external processor.",
							},
						},
					},
				},
			},
		},
	}, nil
}

func (proc *externalProcessor) processRequestBody(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_RequestBody,
) (*ext_procv3.ProcessingResponse_RequestBody, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_RequestBody{
		RequestBody: &ext_procv3.BodyResponse{
			Response: &ext_procv3.CommonResponse{
				Status: ext_procv3.CommonResponse_CONTINUE,
			},
		},
	}, nil
}

func (proc *externalProcessor) processResponseBody(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseBody,
) (*ext_procv3.ProcessingResponse_ResponseBody, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_ResponseBody{
		ResponseBody: &ext_procv3.BodyResponse{
			Response: &ext_procv3.CommonResponse{
				Status: ext_procv3.CommonResponse_CONTINUE,
			},
		},
	}, nil
}

func (proc *externalProcessor) processRequestTrailers(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_RequestTrailers,
) (*ext_procv3.ProcessingResponse_RequestTrailers, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_RequestTrailers{
		RequestTrailers: &ext_procv3.TrailersResponse{},
	}, nil
}

func (proc *externalProcessor) processResponseTrailers(
	_ context.Context,
	_ *ext_procv3.ProcessingRequest_ResponseTrailers,
) (*ext_procv3.ProcessingResponse_ResponseTrailers, error) { //nolint:unparam
	return &ext_procv3.ProcessingResponse_ResponseTrailers{
		ResponseTrailers: &ext_procv3.TrailersResponse{},
	}, nil
}
