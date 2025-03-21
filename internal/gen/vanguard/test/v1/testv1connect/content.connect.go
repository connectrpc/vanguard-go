// Copyright 2023-2025 Buf Technologies, Inc.
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

// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: vanguard/test/v1/content.proto

package testv1connect

import (
	connect "connectrpc.com/connect"
	v1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	context "context"
	errors "errors"
	httpbody "google.golang.org/genproto/googleapis/api/httpbody"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect.IsAtLeastVersion1_13_0

const (
	// ContentServiceName is the fully-qualified name of the ContentService service.
	ContentServiceName = "vanguard.test.v1.ContentService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// ContentServiceIndexProcedure is the fully-qualified name of the ContentService's Index RPC.
	ContentServiceIndexProcedure = "/vanguard.test.v1.ContentService/Index"
	// ContentServiceUploadProcedure is the fully-qualified name of the ContentService's Upload RPC.
	ContentServiceUploadProcedure = "/vanguard.test.v1.ContentService/Upload"
	// ContentServiceDownloadProcedure is the fully-qualified name of the ContentService's Download RPC.
	ContentServiceDownloadProcedure = "/vanguard.test.v1.ContentService/Download"
	// ContentServiceSubscribeProcedure is the fully-qualified name of the ContentService's Subscribe
	// RPC.
	ContentServiceSubscribeProcedure = "/vanguard.test.v1.ContentService/Subscribe"
)

// ContentServiceClient is a client for the vanguard.test.v1.ContentService service.
type ContentServiceClient interface {
	// Index returns a html index page at the given path.
	Index(context.Context, *connect.Request[v1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error)
	// Upload a file to the given path.
	Upload(context.Context) *connect.ClientStreamForClient[v1.UploadRequest, emptypb.Empty]
	// Download a file from the given path.
	Download(context.Context, *connect.Request[v1.DownloadRequest]) (*connect.ServerStreamForClient[v1.DownloadResponse], error)
	// Subscribe to updates for changes to content.
	Subscribe(context.Context) *connect.BidiStreamForClient[v1.SubscribeRequest, v1.SubscribeResponse]
}

// NewContentServiceClient constructs a client for the vanguard.test.v1.ContentService service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewContentServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) ContentServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	contentServiceMethods := v1.File_vanguard_test_v1_content_proto.Services().ByName("ContentService").Methods()
	return &contentServiceClient{
		index: connect.NewClient[v1.IndexRequest, httpbody.HttpBody](
			httpClient,
			baseURL+ContentServiceIndexProcedure,
			connect.WithSchema(contentServiceMethods.ByName("Index")),
			connect.WithClientOptions(opts...),
		),
		upload: connect.NewClient[v1.UploadRequest, emptypb.Empty](
			httpClient,
			baseURL+ContentServiceUploadProcedure,
			connect.WithSchema(contentServiceMethods.ByName("Upload")),
			connect.WithClientOptions(opts...),
		),
		download: connect.NewClient[v1.DownloadRequest, v1.DownloadResponse](
			httpClient,
			baseURL+ContentServiceDownloadProcedure,
			connect.WithSchema(contentServiceMethods.ByName("Download")),
			connect.WithClientOptions(opts...),
		),
		subscribe: connect.NewClient[v1.SubscribeRequest, v1.SubscribeResponse](
			httpClient,
			baseURL+ContentServiceSubscribeProcedure,
			connect.WithSchema(contentServiceMethods.ByName("Subscribe")),
			connect.WithClientOptions(opts...),
		),
	}
}

// contentServiceClient implements ContentServiceClient.
type contentServiceClient struct {
	index     *connect.Client[v1.IndexRequest, httpbody.HttpBody]
	upload    *connect.Client[v1.UploadRequest, emptypb.Empty]
	download  *connect.Client[v1.DownloadRequest, v1.DownloadResponse]
	subscribe *connect.Client[v1.SubscribeRequest, v1.SubscribeResponse]
}

// Index calls vanguard.test.v1.ContentService.Index.
func (c *contentServiceClient) Index(ctx context.Context, req *connect.Request[v1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error) {
	return c.index.CallUnary(ctx, req)
}

// Upload calls vanguard.test.v1.ContentService.Upload.
func (c *contentServiceClient) Upload(ctx context.Context) *connect.ClientStreamForClient[v1.UploadRequest, emptypb.Empty] {
	return c.upload.CallClientStream(ctx)
}

// Download calls vanguard.test.v1.ContentService.Download.
func (c *contentServiceClient) Download(ctx context.Context, req *connect.Request[v1.DownloadRequest]) (*connect.ServerStreamForClient[v1.DownloadResponse], error) {
	return c.download.CallServerStream(ctx, req)
}

// Subscribe calls vanguard.test.v1.ContentService.Subscribe.
func (c *contentServiceClient) Subscribe(ctx context.Context) *connect.BidiStreamForClient[v1.SubscribeRequest, v1.SubscribeResponse] {
	return c.subscribe.CallBidiStream(ctx)
}

// ContentServiceHandler is an implementation of the vanguard.test.v1.ContentService service.
type ContentServiceHandler interface {
	// Index returns a html index page at the given path.
	Index(context.Context, *connect.Request[v1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error)
	// Upload a file to the given path.
	Upload(context.Context, *connect.ClientStream[v1.UploadRequest]) (*connect.Response[emptypb.Empty], error)
	// Download a file from the given path.
	Download(context.Context, *connect.Request[v1.DownloadRequest], *connect.ServerStream[v1.DownloadResponse]) error
	// Subscribe to updates for changes to content.
	Subscribe(context.Context, *connect.BidiStream[v1.SubscribeRequest, v1.SubscribeResponse]) error
}

// NewContentServiceHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewContentServiceHandler(svc ContentServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	contentServiceMethods := v1.File_vanguard_test_v1_content_proto.Services().ByName("ContentService").Methods()
	contentServiceIndexHandler := connect.NewUnaryHandler(
		ContentServiceIndexProcedure,
		svc.Index,
		connect.WithSchema(contentServiceMethods.ByName("Index")),
		connect.WithHandlerOptions(opts...),
	)
	contentServiceUploadHandler := connect.NewClientStreamHandler(
		ContentServiceUploadProcedure,
		svc.Upload,
		connect.WithSchema(contentServiceMethods.ByName("Upload")),
		connect.WithHandlerOptions(opts...),
	)
	contentServiceDownloadHandler := connect.NewServerStreamHandler(
		ContentServiceDownloadProcedure,
		svc.Download,
		connect.WithSchema(contentServiceMethods.ByName("Download")),
		connect.WithHandlerOptions(opts...),
	)
	contentServiceSubscribeHandler := connect.NewBidiStreamHandler(
		ContentServiceSubscribeProcedure,
		svc.Subscribe,
		connect.WithSchema(contentServiceMethods.ByName("Subscribe")),
		connect.WithHandlerOptions(opts...),
	)
	return "/vanguard.test.v1.ContentService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ContentServiceIndexProcedure:
			contentServiceIndexHandler.ServeHTTP(w, r)
		case ContentServiceUploadProcedure:
			contentServiceUploadHandler.ServeHTTP(w, r)
		case ContentServiceDownloadProcedure:
			contentServiceDownloadHandler.ServeHTTP(w, r)
		case ContentServiceSubscribeProcedure:
			contentServiceSubscribeHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedContentServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedContentServiceHandler struct{}

func (UnimplementedContentServiceHandler) Index(context.Context, *connect.Request[v1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.ContentService.Index is not implemented"))
}

func (UnimplementedContentServiceHandler) Upload(context.Context, *connect.ClientStream[v1.UploadRequest]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.ContentService.Upload is not implemented"))
}

func (UnimplementedContentServiceHandler) Download(context.Context, *connect.Request[v1.DownloadRequest], *connect.ServerStream[v1.DownloadResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.ContentService.Download is not implemented"))
}

func (UnimplementedContentServiceHandler) Subscribe(context.Context, *connect.BidiStream[v1.SubscribeRequest, v1.SubscribeResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.ContentService.Subscribe is not implemented"))
}
