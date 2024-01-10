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

// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: vanguard/test/v1/library.proto

package testv1connect

import (
	connect "connectrpc.com/connect"
	v1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	context "context"
	errors "errors"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect.IsAtLeastVersion1_7_0

const (
	// LibraryServiceName is the fully-qualified name of the LibraryService service.
	LibraryServiceName = "vanguard.test.v1.LibraryService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// LibraryServiceGetBookProcedure is the fully-qualified name of the LibraryService's GetBook RPC.
	LibraryServiceGetBookProcedure = "/vanguard.test.v1.LibraryService/GetBook"
	// LibraryServiceCreateBookProcedure is the fully-qualified name of the LibraryService's CreateBook
	// RPC.
	LibraryServiceCreateBookProcedure = "/vanguard.test.v1.LibraryService/CreateBook"
	// LibraryServiceListBooksProcedure is the fully-qualified name of the LibraryService's ListBooks
	// RPC.
	LibraryServiceListBooksProcedure = "/vanguard.test.v1.LibraryService/ListBooks"
	// LibraryServiceCreateShelfProcedure is the fully-qualified name of the LibraryService's
	// CreateShelf RPC.
	LibraryServiceCreateShelfProcedure = "/vanguard.test.v1.LibraryService/CreateShelf"
	// LibraryServiceUpdateBookProcedure is the fully-qualified name of the LibraryService's UpdateBook
	// RPC.
	LibraryServiceUpdateBookProcedure = "/vanguard.test.v1.LibraryService/UpdateBook"
	// LibraryServiceDeleteBookProcedure is the fully-qualified name of the LibraryService's DeleteBook
	// RPC.
	LibraryServiceDeleteBookProcedure = "/vanguard.test.v1.LibraryService/DeleteBook"
	// LibraryServiceSearchBooksProcedure is the fully-qualified name of the LibraryService's
	// SearchBooks RPC.
	LibraryServiceSearchBooksProcedure = "/vanguard.test.v1.LibraryService/SearchBooks"
	// LibraryServiceMoveBooksProcedure is the fully-qualified name of the LibraryService's MoveBooks
	// RPC.
	LibraryServiceMoveBooksProcedure = "/vanguard.test.v1.LibraryService/MoveBooks"
	// LibraryServiceCheckoutBooksProcedure is the fully-qualified name of the LibraryService's
	// CheckoutBooks RPC.
	LibraryServiceCheckoutBooksProcedure = "/vanguard.test.v1.LibraryService/CheckoutBooks"
	// LibraryServiceReturnBooksProcedure is the fully-qualified name of the LibraryService's
	// ReturnBooks RPC.
	LibraryServiceReturnBooksProcedure = "/vanguard.test.v1.LibraryService/ReturnBooks"
	// LibraryServiceGetCheckoutProcedure is the fully-qualified name of the LibraryService's
	// GetCheckout RPC.
	LibraryServiceGetCheckoutProcedure = "/vanguard.test.v1.LibraryService/GetCheckout"
	// LibraryServiceListCheckoutsProcedure is the fully-qualified name of the LibraryService's
	// ListCheckouts RPC.
	LibraryServiceListCheckoutsProcedure = "/vanguard.test.v1.LibraryService/ListCheckouts"
)

// LibraryServiceClient is a client for the vanguard.test.v1.LibraryService service.
type LibraryServiceClient interface {
	// Gets a book.
	GetBook(context.Context, *connect.Request[v1.GetBookRequest]) (*connect.Response[v1.Book], error)
	// Creates a book, and returns the new Book.
	CreateBook(context.Context, *connect.Request[v1.CreateBookRequest]) (*connect.Response[v1.Book], error)
	// Lists books in a shelf.
	ListBooks(context.Context, *connect.Request[v1.ListBooksRequest]) (*connect.Response[v1.ListBooksResponse], error)
	// Creates a shelf.
	CreateShelf(context.Context, *connect.Request[v1.CreateShelfRequest]) (*connect.Response[v1.Shelf], error)
	// Updates a book.
	UpdateBook(context.Context, *connect.Request[v1.UpdateBookRequest]) (*connect.Response[v1.Book], error)
	// Deletes a book.
	DeleteBook(context.Context, *connect.Request[v1.DeleteBookRequest]) (*connect.Response[emptypb.Empty], error)
	// Search books in a shelf.
	SearchBooks(context.Context, *connect.Request[v1.SearchBooksRequest]) (*connect.Response[v1.SearchBooksResponse], error)
	MoveBooks(context.Context, *connect.Request[v1.MoveBooksRequest]) (*connect.Response[v1.MoveBooksResponse], error)
	CheckoutBooks(context.Context, *connect.Request[v1.CheckoutBooksRequest]) (*connect.Response[v1.Checkout], error)
	ReturnBooks(context.Context, *connect.Request[v1.ReturnBooksRequest]) (*connect.Response[emptypb.Empty], error)
	GetCheckout(context.Context, *connect.Request[v1.GetCheckoutRequest]) (*connect.Response[v1.Checkout], error)
	ListCheckouts(context.Context, *connect.Request[v1.ListCheckoutsRequest]) (*connect.Response[v1.ListCheckoutsResponse], error)
}

// NewLibraryServiceClient constructs a client for the vanguard.test.v1.LibraryService service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewLibraryServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) LibraryServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &libraryServiceClient{
		getBook: connect.NewClient[v1.GetBookRequest, v1.Book](
			httpClient,
			baseURL+LibraryServiceGetBookProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
		createBook: connect.NewClient[v1.CreateBookRequest, v1.Book](
			httpClient,
			baseURL+LibraryServiceCreateBookProcedure,
			opts...,
		),
		listBooks: connect.NewClient[v1.ListBooksRequest, v1.ListBooksResponse](
			httpClient,
			baseURL+LibraryServiceListBooksProcedure,
			opts...,
		),
		createShelf: connect.NewClient[v1.CreateShelfRequest, v1.Shelf](
			httpClient,
			baseURL+LibraryServiceCreateShelfProcedure,
			opts...,
		),
		updateBook: connect.NewClient[v1.UpdateBookRequest, v1.Book](
			httpClient,
			baseURL+LibraryServiceUpdateBookProcedure,
			opts...,
		),
		deleteBook: connect.NewClient[v1.DeleteBookRequest, emptypb.Empty](
			httpClient,
			baseURL+LibraryServiceDeleteBookProcedure,
			opts...,
		),
		searchBooks: connect.NewClient[v1.SearchBooksRequest, v1.SearchBooksResponse](
			httpClient,
			baseURL+LibraryServiceSearchBooksProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
		moveBooks: connect.NewClient[v1.MoveBooksRequest, v1.MoveBooksResponse](
			httpClient,
			baseURL+LibraryServiceMoveBooksProcedure,
			opts...,
		),
		checkoutBooks: connect.NewClient[v1.CheckoutBooksRequest, v1.Checkout](
			httpClient,
			baseURL+LibraryServiceCheckoutBooksProcedure,
			opts...,
		),
		returnBooks: connect.NewClient[v1.ReturnBooksRequest, emptypb.Empty](
			httpClient,
			baseURL+LibraryServiceReturnBooksProcedure,
			opts...,
		),
		getCheckout: connect.NewClient[v1.GetCheckoutRequest, v1.Checkout](
			httpClient,
			baseURL+LibraryServiceGetCheckoutProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
		listCheckouts: connect.NewClient[v1.ListCheckoutsRequest, v1.ListCheckoutsResponse](
			httpClient,
			baseURL+LibraryServiceListCheckoutsProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
	}
}

// libraryServiceClient implements LibraryServiceClient.
type libraryServiceClient struct {
	getBook       *connect.Client[v1.GetBookRequest, v1.Book]
	createBook    *connect.Client[v1.CreateBookRequest, v1.Book]
	listBooks     *connect.Client[v1.ListBooksRequest, v1.ListBooksResponse]
	createShelf   *connect.Client[v1.CreateShelfRequest, v1.Shelf]
	updateBook    *connect.Client[v1.UpdateBookRequest, v1.Book]
	deleteBook    *connect.Client[v1.DeleteBookRequest, emptypb.Empty]
	searchBooks   *connect.Client[v1.SearchBooksRequest, v1.SearchBooksResponse]
	moveBooks     *connect.Client[v1.MoveBooksRequest, v1.MoveBooksResponse]
	checkoutBooks *connect.Client[v1.CheckoutBooksRequest, v1.Checkout]
	returnBooks   *connect.Client[v1.ReturnBooksRequest, emptypb.Empty]
	getCheckout   *connect.Client[v1.GetCheckoutRequest, v1.Checkout]
	listCheckouts *connect.Client[v1.ListCheckoutsRequest, v1.ListCheckoutsResponse]
}

// GetBook calls vanguard.test.v1.LibraryService.GetBook.
func (c *libraryServiceClient) GetBook(ctx context.Context, req *connect.Request[v1.GetBookRequest]) (*connect.Response[v1.Book], error) {
	return c.getBook.CallUnary(ctx, req)
}

// CreateBook calls vanguard.test.v1.LibraryService.CreateBook.
func (c *libraryServiceClient) CreateBook(ctx context.Context, req *connect.Request[v1.CreateBookRequest]) (*connect.Response[v1.Book], error) {
	return c.createBook.CallUnary(ctx, req)
}

// ListBooks calls vanguard.test.v1.LibraryService.ListBooks.
func (c *libraryServiceClient) ListBooks(ctx context.Context, req *connect.Request[v1.ListBooksRequest]) (*connect.Response[v1.ListBooksResponse], error) {
	return c.listBooks.CallUnary(ctx, req)
}

// CreateShelf calls vanguard.test.v1.LibraryService.CreateShelf.
func (c *libraryServiceClient) CreateShelf(ctx context.Context, req *connect.Request[v1.CreateShelfRequest]) (*connect.Response[v1.Shelf], error) {
	return c.createShelf.CallUnary(ctx, req)
}

// UpdateBook calls vanguard.test.v1.LibraryService.UpdateBook.
func (c *libraryServiceClient) UpdateBook(ctx context.Context, req *connect.Request[v1.UpdateBookRequest]) (*connect.Response[v1.Book], error) {
	return c.updateBook.CallUnary(ctx, req)
}

// DeleteBook calls vanguard.test.v1.LibraryService.DeleteBook.
func (c *libraryServiceClient) DeleteBook(ctx context.Context, req *connect.Request[v1.DeleteBookRequest]) (*connect.Response[emptypb.Empty], error) {
	return c.deleteBook.CallUnary(ctx, req)
}

// SearchBooks calls vanguard.test.v1.LibraryService.SearchBooks.
func (c *libraryServiceClient) SearchBooks(ctx context.Context, req *connect.Request[v1.SearchBooksRequest]) (*connect.Response[v1.SearchBooksResponse], error) {
	return c.searchBooks.CallUnary(ctx, req)
}

// MoveBooks calls vanguard.test.v1.LibraryService.MoveBooks.
func (c *libraryServiceClient) MoveBooks(ctx context.Context, req *connect.Request[v1.MoveBooksRequest]) (*connect.Response[v1.MoveBooksResponse], error) {
	return c.moveBooks.CallUnary(ctx, req)
}

// CheckoutBooks calls vanguard.test.v1.LibraryService.CheckoutBooks.
func (c *libraryServiceClient) CheckoutBooks(ctx context.Context, req *connect.Request[v1.CheckoutBooksRequest]) (*connect.Response[v1.Checkout], error) {
	return c.checkoutBooks.CallUnary(ctx, req)
}

// ReturnBooks calls vanguard.test.v1.LibraryService.ReturnBooks.
func (c *libraryServiceClient) ReturnBooks(ctx context.Context, req *connect.Request[v1.ReturnBooksRequest]) (*connect.Response[emptypb.Empty], error) {
	return c.returnBooks.CallUnary(ctx, req)
}

// GetCheckout calls vanguard.test.v1.LibraryService.GetCheckout.
func (c *libraryServiceClient) GetCheckout(ctx context.Context, req *connect.Request[v1.GetCheckoutRequest]) (*connect.Response[v1.Checkout], error) {
	return c.getCheckout.CallUnary(ctx, req)
}

// ListCheckouts calls vanguard.test.v1.LibraryService.ListCheckouts.
func (c *libraryServiceClient) ListCheckouts(ctx context.Context, req *connect.Request[v1.ListCheckoutsRequest]) (*connect.Response[v1.ListCheckoutsResponse], error) {
	return c.listCheckouts.CallUnary(ctx, req)
}

// LibraryServiceHandler is an implementation of the vanguard.test.v1.LibraryService service.
type LibraryServiceHandler interface {
	// Gets a book.
	GetBook(context.Context, *connect.Request[v1.GetBookRequest]) (*connect.Response[v1.Book], error)
	// Creates a book, and returns the new Book.
	CreateBook(context.Context, *connect.Request[v1.CreateBookRequest]) (*connect.Response[v1.Book], error)
	// Lists books in a shelf.
	ListBooks(context.Context, *connect.Request[v1.ListBooksRequest]) (*connect.Response[v1.ListBooksResponse], error)
	// Creates a shelf.
	CreateShelf(context.Context, *connect.Request[v1.CreateShelfRequest]) (*connect.Response[v1.Shelf], error)
	// Updates a book.
	UpdateBook(context.Context, *connect.Request[v1.UpdateBookRequest]) (*connect.Response[v1.Book], error)
	// Deletes a book.
	DeleteBook(context.Context, *connect.Request[v1.DeleteBookRequest]) (*connect.Response[emptypb.Empty], error)
	// Search books in a shelf.
	SearchBooks(context.Context, *connect.Request[v1.SearchBooksRequest]) (*connect.Response[v1.SearchBooksResponse], error)
	MoveBooks(context.Context, *connect.Request[v1.MoveBooksRequest]) (*connect.Response[v1.MoveBooksResponse], error)
	CheckoutBooks(context.Context, *connect.Request[v1.CheckoutBooksRequest]) (*connect.Response[v1.Checkout], error)
	ReturnBooks(context.Context, *connect.Request[v1.ReturnBooksRequest]) (*connect.Response[emptypb.Empty], error)
	GetCheckout(context.Context, *connect.Request[v1.GetCheckoutRequest]) (*connect.Response[v1.Checkout], error)
	ListCheckouts(context.Context, *connect.Request[v1.ListCheckoutsRequest]) (*connect.Response[v1.ListCheckoutsResponse], error)
}

// NewLibraryServiceHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewLibraryServiceHandler(svc LibraryServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	libraryServiceGetBookHandler := connect.NewUnaryHandler(
		LibraryServiceGetBookProcedure,
		svc.GetBook,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	libraryServiceCreateBookHandler := connect.NewUnaryHandler(
		LibraryServiceCreateBookProcedure,
		svc.CreateBook,
		opts...,
	)
	libraryServiceListBooksHandler := connect.NewUnaryHandler(
		LibraryServiceListBooksProcedure,
		svc.ListBooks,
		opts...,
	)
	libraryServiceCreateShelfHandler := connect.NewUnaryHandler(
		LibraryServiceCreateShelfProcedure,
		svc.CreateShelf,
		opts...,
	)
	libraryServiceUpdateBookHandler := connect.NewUnaryHandler(
		LibraryServiceUpdateBookProcedure,
		svc.UpdateBook,
		opts...,
	)
	libraryServiceDeleteBookHandler := connect.NewUnaryHandler(
		LibraryServiceDeleteBookProcedure,
		svc.DeleteBook,
		opts...,
	)
	libraryServiceSearchBooksHandler := connect.NewUnaryHandler(
		LibraryServiceSearchBooksProcedure,
		svc.SearchBooks,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	libraryServiceMoveBooksHandler := connect.NewUnaryHandler(
		LibraryServiceMoveBooksProcedure,
		svc.MoveBooks,
		opts...,
	)
	libraryServiceCheckoutBooksHandler := connect.NewUnaryHandler(
		LibraryServiceCheckoutBooksProcedure,
		svc.CheckoutBooks,
		opts...,
	)
	libraryServiceReturnBooksHandler := connect.NewUnaryHandler(
		LibraryServiceReturnBooksProcedure,
		svc.ReturnBooks,
		opts...,
	)
	libraryServiceGetCheckoutHandler := connect.NewUnaryHandler(
		LibraryServiceGetCheckoutProcedure,
		svc.GetCheckout,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	libraryServiceListCheckoutsHandler := connect.NewUnaryHandler(
		LibraryServiceListCheckoutsProcedure,
		svc.ListCheckouts,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	return "/vanguard.test.v1.LibraryService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case LibraryServiceGetBookProcedure:
			libraryServiceGetBookHandler.ServeHTTP(w, r)
		case LibraryServiceCreateBookProcedure:
			libraryServiceCreateBookHandler.ServeHTTP(w, r)
		case LibraryServiceListBooksProcedure:
			libraryServiceListBooksHandler.ServeHTTP(w, r)
		case LibraryServiceCreateShelfProcedure:
			libraryServiceCreateShelfHandler.ServeHTTP(w, r)
		case LibraryServiceUpdateBookProcedure:
			libraryServiceUpdateBookHandler.ServeHTTP(w, r)
		case LibraryServiceDeleteBookProcedure:
			libraryServiceDeleteBookHandler.ServeHTTP(w, r)
		case LibraryServiceSearchBooksProcedure:
			libraryServiceSearchBooksHandler.ServeHTTP(w, r)
		case LibraryServiceMoveBooksProcedure:
			libraryServiceMoveBooksHandler.ServeHTTP(w, r)
		case LibraryServiceCheckoutBooksProcedure:
			libraryServiceCheckoutBooksHandler.ServeHTTP(w, r)
		case LibraryServiceReturnBooksProcedure:
			libraryServiceReturnBooksHandler.ServeHTTP(w, r)
		case LibraryServiceGetCheckoutProcedure:
			libraryServiceGetCheckoutHandler.ServeHTTP(w, r)
		case LibraryServiceListCheckoutsProcedure:
			libraryServiceListCheckoutsHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedLibraryServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedLibraryServiceHandler struct{}

func (UnimplementedLibraryServiceHandler) GetBook(context.Context, *connect.Request[v1.GetBookRequest]) (*connect.Response[v1.Book], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.GetBook is not implemented"))
}

func (UnimplementedLibraryServiceHandler) CreateBook(context.Context, *connect.Request[v1.CreateBookRequest]) (*connect.Response[v1.Book], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.CreateBook is not implemented"))
}

func (UnimplementedLibraryServiceHandler) ListBooks(context.Context, *connect.Request[v1.ListBooksRequest]) (*connect.Response[v1.ListBooksResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.ListBooks is not implemented"))
}

func (UnimplementedLibraryServiceHandler) CreateShelf(context.Context, *connect.Request[v1.CreateShelfRequest]) (*connect.Response[v1.Shelf], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.CreateShelf is not implemented"))
}

func (UnimplementedLibraryServiceHandler) UpdateBook(context.Context, *connect.Request[v1.UpdateBookRequest]) (*connect.Response[v1.Book], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.UpdateBook is not implemented"))
}

func (UnimplementedLibraryServiceHandler) DeleteBook(context.Context, *connect.Request[v1.DeleteBookRequest]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.DeleteBook is not implemented"))
}

func (UnimplementedLibraryServiceHandler) SearchBooks(context.Context, *connect.Request[v1.SearchBooksRequest]) (*connect.Response[v1.SearchBooksResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.SearchBooks is not implemented"))
}

func (UnimplementedLibraryServiceHandler) MoveBooks(context.Context, *connect.Request[v1.MoveBooksRequest]) (*connect.Response[v1.MoveBooksResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.MoveBooks is not implemented"))
}

func (UnimplementedLibraryServiceHandler) CheckoutBooks(context.Context, *connect.Request[v1.CheckoutBooksRequest]) (*connect.Response[v1.Checkout], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.CheckoutBooks is not implemented"))
}

func (UnimplementedLibraryServiceHandler) ReturnBooks(context.Context, *connect.Request[v1.ReturnBooksRequest]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.ReturnBooks is not implemented"))
}

func (UnimplementedLibraryServiceHandler) GetCheckout(context.Context, *connect.Request[v1.GetCheckoutRequest]) (*connect.Response[v1.Checkout], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.GetCheckout is not implemented"))
}

func (UnimplementedLibraryServiceHandler) ListCheckouts(context.Context, *connect.Request[v1.ListCheckoutsRequest]) (*connect.Response[v1.ListCheckoutsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("vanguard.test.v1.LibraryService.ListCheckouts is not implemented"))
}
