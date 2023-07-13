package extproc

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"

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

var _ ext_procv3connect.ExternalProcessorHandler = &externalProcessor{}

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
		Addr:      ":http",
		TLSConfig: proc.tlsConfig,
		Handler:   h2c.NewHandler(handler, h2Server),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		// TODO: timeouts, etc?
	}
	http2.ConfigureServer(server, h2Server)

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

func (e *externalProcessor) Process(
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
			response, err = e.processRequestHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseHeaders:
			response, err = e.processResponseHeaders(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestBody:
			response, err = e.processRequestBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseBody:
			response, err = e.processResponseBody(ctx, requestType)
		case *ext_procv3.ProcessingRequest_RequestTrailers:
			response, err = e.processRequestTrailers(ctx, requestType)
		case *ext_procv3.ProcessingRequest_ResponseTrailers:
			response, err = e.processResponseTrailers(ctx, requestType)
		default:
			err = connect.NewError(connect.CodeInvalidArgument, errors.New("unknown request type"))
		}
		if err != nil {
			return err
		}
		stream.Send(response)
	}
}

func (e *externalProcessor) processRequestHeaders(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_RequestHeaders,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (e *externalProcessor) processResponseHeaders(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_ResponseHeaders,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (e *externalProcessor) processRequestBody(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_RequestBody,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (e *externalProcessor) processResponseBody(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_ResponseBody,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (e *externalProcessor) processRequestTrailers(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_RequestTrailers,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (e *externalProcessor) processResponseTrailers(
	ctx context.Context,
	request *ext_procv3.ProcessingRequest_ResponseTrailers,
) (*ext_procv3.ProcessingResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
