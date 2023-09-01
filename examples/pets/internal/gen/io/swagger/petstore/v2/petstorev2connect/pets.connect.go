// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: io/swagger/petstore/v2/pets.proto

package petstorev2connect

import (
	connect "connectrpc.com/connect"
	context "context"
	errors "errors"
	v2 "github.com/bufbuild/vanguard-go/examples/pets/internal/gen/io/swagger/petstore/v2"
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
	// PetServiceName is the fully-qualified name of the PetService service.
	PetServiceName = "io.swagger.petstore.v2.PetService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// PetServiceGetPetByIDProcedure is the fully-qualified name of the PetService's GetPetByID RPC.
	PetServiceGetPetByIDProcedure = "/io.swagger.petstore.v2.PetService/GetPetByID"
	// PetServiceUpdatePetWithFormProcedure is the fully-qualified name of the PetService's
	// UpdatePetWithForm RPC.
	PetServiceUpdatePetWithFormProcedure = "/io.swagger.petstore.v2.PetService/UpdatePetWithForm"
	// PetServiceDeletePetProcedure is the fully-qualified name of the PetService's DeletePet RPC.
	PetServiceDeletePetProcedure = "/io.swagger.petstore.v2.PetService/DeletePet"
	// PetServiceUploadFileProcedure is the fully-qualified name of the PetService's UploadFile RPC.
	PetServiceUploadFileProcedure = "/io.swagger.petstore.v2.PetService/UploadFile"
	// PetServiceAddPetProcedure is the fully-qualified name of the PetService's AddPet RPC.
	PetServiceAddPetProcedure = "/io.swagger.petstore.v2.PetService/AddPet"
	// PetServiceUpdatePetProcedure is the fully-qualified name of the PetService's UpdatePet RPC.
	PetServiceUpdatePetProcedure = "/io.swagger.petstore.v2.PetService/UpdatePet"
	// PetServiceFindPetsByTagProcedure is the fully-qualified name of the PetService's FindPetsByTag
	// RPC.
	PetServiceFindPetsByTagProcedure = "/io.swagger.petstore.v2.PetService/FindPetsByTag"
	// PetServiceFindPetsByStatusProcedure is the fully-qualified name of the PetService's
	// FindPetsByStatus RPC.
	PetServiceFindPetsByStatusProcedure = "/io.swagger.petstore.v2.PetService/FindPetsByStatus"
)

// PetServiceClient is a client for the io.swagger.petstore.v2.PetService service.
type PetServiceClient interface {
	GetPetByID(context.Context, *connect.Request[v2.PetID]) (*connect.Response[v2.Pet], error)
	UpdatePetWithForm(context.Context, *connect.Request[v2.UpdatePetWithFormReq]) (*connect.Response[emptypb.Empty], error)
	DeletePet(context.Context, *connect.Request[v2.PetID]) (*connect.Response[emptypb.Empty], error)
	UploadFile(context.Context, *connect.Request[v2.UploadFileReq]) (*connect.Response[v2.ApiResponse], error)
	AddPet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error)
	UpdatePet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error)
	// Deprecated: do not use.
	FindPetsByTag(context.Context, *connect.Request[v2.TagReq]) (*connect.Response[v2.Pets], error)
	FindPetsByStatus(context.Context, *connect.Request[v2.StatusReq]) (*connect.Response[v2.Pets], error)
}

// NewPetServiceClient constructs a client for the io.swagger.petstore.v2.PetService service. By
// default, it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses,
// and sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the
// connect.WithGRPC() or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewPetServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PetServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &petServiceClient{
		getPetByID: connect.NewClient[v2.PetID, v2.Pet](
			httpClient,
			baseURL+PetServiceGetPetByIDProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
		updatePetWithForm: connect.NewClient[v2.UpdatePetWithFormReq, emptypb.Empty](
			httpClient,
			baseURL+PetServiceUpdatePetWithFormProcedure,
			opts...,
		),
		deletePet: connect.NewClient[v2.PetID, emptypb.Empty](
			httpClient,
			baseURL+PetServiceDeletePetProcedure,
			opts...,
		),
		uploadFile: connect.NewClient[v2.UploadFileReq, v2.ApiResponse](
			httpClient,
			baseURL+PetServiceUploadFileProcedure,
			opts...,
		),
		addPet: connect.NewClient[v2.Pet, v2.Pet](
			httpClient,
			baseURL+PetServiceAddPetProcedure,
			opts...,
		),
		updatePet: connect.NewClient[v2.Pet, v2.Pet](
			httpClient,
			baseURL+PetServiceUpdatePetProcedure,
			opts...,
		),
		findPetsByTag: connect.NewClient[v2.TagReq, v2.Pets](
			httpClient,
			baseURL+PetServiceFindPetsByTagProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
		findPetsByStatus: connect.NewClient[v2.StatusReq, v2.Pets](
			httpClient,
			baseURL+PetServiceFindPetsByStatusProcedure,
			connect.WithIdempotency(connect.IdempotencyNoSideEffects),
			connect.WithClientOptions(opts...),
		),
	}
}

// petServiceClient implements PetServiceClient.
type petServiceClient struct {
	getPetByID        *connect.Client[v2.PetID, v2.Pet]
	updatePetWithForm *connect.Client[v2.UpdatePetWithFormReq, emptypb.Empty]
	deletePet         *connect.Client[v2.PetID, emptypb.Empty]
	uploadFile        *connect.Client[v2.UploadFileReq, v2.ApiResponse]
	addPet            *connect.Client[v2.Pet, v2.Pet]
	updatePet         *connect.Client[v2.Pet, v2.Pet]
	findPetsByTag     *connect.Client[v2.TagReq, v2.Pets]
	findPetsByStatus  *connect.Client[v2.StatusReq, v2.Pets]
}

// GetPetByID calls io.swagger.petstore.v2.PetService.GetPetByID.
func (c *petServiceClient) GetPetByID(ctx context.Context, req *connect.Request[v2.PetID]) (*connect.Response[v2.Pet], error) {
	return c.getPetByID.CallUnary(ctx, req)
}

// UpdatePetWithForm calls io.swagger.petstore.v2.PetService.UpdatePetWithForm.
func (c *petServiceClient) UpdatePetWithForm(ctx context.Context, req *connect.Request[v2.UpdatePetWithFormReq]) (*connect.Response[emptypb.Empty], error) {
	return c.updatePetWithForm.CallUnary(ctx, req)
}

// DeletePet calls io.swagger.petstore.v2.PetService.DeletePet.
func (c *petServiceClient) DeletePet(ctx context.Context, req *connect.Request[v2.PetID]) (*connect.Response[emptypb.Empty], error) {
	return c.deletePet.CallUnary(ctx, req)
}

// UploadFile calls io.swagger.petstore.v2.PetService.UploadFile.
func (c *petServiceClient) UploadFile(ctx context.Context, req *connect.Request[v2.UploadFileReq]) (*connect.Response[v2.ApiResponse], error) {
	return c.uploadFile.CallUnary(ctx, req)
}

// AddPet calls io.swagger.petstore.v2.PetService.AddPet.
func (c *petServiceClient) AddPet(ctx context.Context, req *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error) {
	return c.addPet.CallUnary(ctx, req)
}

// UpdatePet calls io.swagger.petstore.v2.PetService.UpdatePet.
func (c *petServiceClient) UpdatePet(ctx context.Context, req *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error) {
	return c.updatePet.CallUnary(ctx, req)
}

// FindPetsByTag calls io.swagger.petstore.v2.PetService.FindPetsByTag.
//
// Deprecated: do not use.
func (c *petServiceClient) FindPetsByTag(ctx context.Context, req *connect.Request[v2.TagReq]) (*connect.Response[v2.Pets], error) {
	return c.findPetsByTag.CallUnary(ctx, req)
}

// FindPetsByStatus calls io.swagger.petstore.v2.PetService.FindPetsByStatus.
func (c *petServiceClient) FindPetsByStatus(ctx context.Context, req *connect.Request[v2.StatusReq]) (*connect.Response[v2.Pets], error) {
	return c.findPetsByStatus.CallUnary(ctx, req)
}

// PetServiceHandler is an implementation of the io.swagger.petstore.v2.PetService service.
type PetServiceHandler interface {
	GetPetByID(context.Context, *connect.Request[v2.PetID]) (*connect.Response[v2.Pet], error)
	UpdatePetWithForm(context.Context, *connect.Request[v2.UpdatePetWithFormReq]) (*connect.Response[emptypb.Empty], error)
	DeletePet(context.Context, *connect.Request[v2.PetID]) (*connect.Response[emptypb.Empty], error)
	UploadFile(context.Context, *connect.Request[v2.UploadFileReq]) (*connect.Response[v2.ApiResponse], error)
	AddPet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error)
	UpdatePet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error)
	// Deprecated: do not use.
	FindPetsByTag(context.Context, *connect.Request[v2.TagReq]) (*connect.Response[v2.Pets], error)
	FindPetsByStatus(context.Context, *connect.Request[v2.StatusReq]) (*connect.Response[v2.Pets], error)
}

// NewPetServiceHandler builds an HTTP handler from the service implementation. It returns the path
// on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewPetServiceHandler(svc PetServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	petServiceGetPetByIDHandler := connect.NewUnaryHandler(
		PetServiceGetPetByIDProcedure,
		svc.GetPetByID,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	petServiceUpdatePetWithFormHandler := connect.NewUnaryHandler(
		PetServiceUpdatePetWithFormProcedure,
		svc.UpdatePetWithForm,
		opts...,
	)
	petServiceDeletePetHandler := connect.NewUnaryHandler(
		PetServiceDeletePetProcedure,
		svc.DeletePet,
		opts...,
	)
	petServiceUploadFileHandler := connect.NewUnaryHandler(
		PetServiceUploadFileProcedure,
		svc.UploadFile,
		opts...,
	)
	petServiceAddPetHandler := connect.NewUnaryHandler(
		PetServiceAddPetProcedure,
		svc.AddPet,
		opts...,
	)
	petServiceUpdatePetHandler := connect.NewUnaryHandler(
		PetServiceUpdatePetProcedure,
		svc.UpdatePet,
		opts...,
	)
	petServiceFindPetsByTagHandler := connect.NewUnaryHandler(
		PetServiceFindPetsByTagProcedure,
		svc.FindPetsByTag,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	petServiceFindPetsByStatusHandler := connect.NewUnaryHandler(
		PetServiceFindPetsByStatusProcedure,
		svc.FindPetsByStatus,
		connect.WithIdempotency(connect.IdempotencyNoSideEffects),
		connect.WithHandlerOptions(opts...),
	)
	return "/io.swagger.petstore.v2.PetService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PetServiceGetPetByIDProcedure:
			petServiceGetPetByIDHandler.ServeHTTP(w, r)
		case PetServiceUpdatePetWithFormProcedure:
			petServiceUpdatePetWithFormHandler.ServeHTTP(w, r)
		case PetServiceDeletePetProcedure:
			petServiceDeletePetHandler.ServeHTTP(w, r)
		case PetServiceUploadFileProcedure:
			petServiceUploadFileHandler.ServeHTTP(w, r)
		case PetServiceAddPetProcedure:
			petServiceAddPetHandler.ServeHTTP(w, r)
		case PetServiceUpdatePetProcedure:
			petServiceUpdatePetHandler.ServeHTTP(w, r)
		case PetServiceFindPetsByTagProcedure:
			petServiceFindPetsByTagHandler.ServeHTTP(w, r)
		case PetServiceFindPetsByStatusProcedure:
			petServiceFindPetsByStatusHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedPetServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedPetServiceHandler struct{}

func (UnimplementedPetServiceHandler) GetPetByID(context.Context, *connect.Request[v2.PetID]) (*connect.Response[v2.Pet], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.GetPetByID is not implemented"))
}

func (UnimplementedPetServiceHandler) UpdatePetWithForm(context.Context, *connect.Request[v2.UpdatePetWithFormReq]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.UpdatePetWithForm is not implemented"))
}

func (UnimplementedPetServiceHandler) DeletePet(context.Context, *connect.Request[v2.PetID]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.DeletePet is not implemented"))
}

func (UnimplementedPetServiceHandler) UploadFile(context.Context, *connect.Request[v2.UploadFileReq]) (*connect.Response[v2.ApiResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.UploadFile is not implemented"))
}

func (UnimplementedPetServiceHandler) AddPet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.AddPet is not implemented"))
}

func (UnimplementedPetServiceHandler) UpdatePet(context.Context, *connect.Request[v2.Pet]) (*connect.Response[v2.Pet], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.UpdatePet is not implemented"))
}

func (UnimplementedPetServiceHandler) FindPetsByTag(context.Context, *connect.Request[v2.TagReq]) (*connect.Response[v2.Pets], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.FindPetsByTag is not implemented"))
}

func (UnimplementedPetServiceHandler) FindPetsByStatus(context.Context, *connect.Request[v2.StatusReq]) (*connect.Response[v2.Pets], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("io.swagger.petstore.v2.PetService.FindPetsByStatus is not implemented"))
}
