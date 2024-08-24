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

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: io/swagger/petstore/v2/pets.proto

// The service defined herein comes from v2 of the Petstore service, which
// is used as an example for Swagger/OpenAPI. The Swagger spec can be found
// here: https://petstore.swagger.io/v2/swagger.json
// A human-friendly HTML view of this API is also available at
// https://petstore.swagger.io.
//
// This file defines only the "pet" service. The spec for this site also
// includes "store" and "user" services which are not supported via these
// RPC definitions.

package petstorev2

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	PetService_GetPetByID_FullMethodName        = "/io.swagger.petstore.v2.PetService/GetPetByID"
	PetService_UpdatePetWithForm_FullMethodName = "/io.swagger.petstore.v2.PetService/UpdatePetWithForm"
	PetService_DeletePet_FullMethodName         = "/io.swagger.petstore.v2.PetService/DeletePet"
	PetService_UploadFile_FullMethodName        = "/io.swagger.petstore.v2.PetService/UploadFile"
	PetService_AddPet_FullMethodName            = "/io.swagger.petstore.v2.PetService/AddPet"
	PetService_UpdatePet_FullMethodName         = "/io.swagger.petstore.v2.PetService/UpdatePet"
	PetService_FindPetsByTag_FullMethodName     = "/io.swagger.petstore.v2.PetService/FindPetsByTag"
	PetService_FindPetsByStatus_FullMethodName  = "/io.swagger.petstore.v2.PetService/FindPetsByStatus"
)

// PetServiceClient is the client API for PetService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type PetServiceClient interface {
	GetPetByID(ctx context.Context, in *PetID, opts ...grpc.CallOption) (*Pet, error)
	UpdatePetWithForm(ctx context.Context, in *UpdatePetWithFormReq, opts ...grpc.CallOption) (*emptypb.Empty, error)
	DeletePet(ctx context.Context, in *PetID, opts ...grpc.CallOption) (*emptypb.Empty, error)
	UploadFile(ctx context.Context, in *UploadFileReq, opts ...grpc.CallOption) (*ApiResponse, error)
	AddPet(ctx context.Context, in *Pet, opts ...grpc.CallOption) (*Pet, error)
	UpdatePet(ctx context.Context, in *Pet, opts ...grpc.CallOption) (*Pet, error)
	// Deprecated: Do not use.
	FindPetsByTag(ctx context.Context, in *TagReq, opts ...grpc.CallOption) (*Pets, error)
	FindPetsByStatus(ctx context.Context, in *StatusReq, opts ...grpc.CallOption) (*Pets, error)
}

type petServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewPetServiceClient(cc grpc.ClientConnInterface) PetServiceClient {
	return &petServiceClient{cc}
}

func (c *petServiceClient) GetPetByID(ctx context.Context, in *PetID, opts ...grpc.CallOption) (*Pet, error) {
	out := new(Pet)
	err := c.cc.Invoke(ctx, PetService_GetPetByID_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) UpdatePetWithForm(ctx context.Context, in *UpdatePetWithFormReq, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, PetService_UpdatePetWithForm_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) DeletePet(ctx context.Context, in *PetID, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, PetService_DeletePet_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) UploadFile(ctx context.Context, in *UploadFileReq, opts ...grpc.CallOption) (*ApiResponse, error) {
	out := new(ApiResponse)
	err := c.cc.Invoke(ctx, PetService_UploadFile_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) AddPet(ctx context.Context, in *Pet, opts ...grpc.CallOption) (*Pet, error) {
	out := new(Pet)
	err := c.cc.Invoke(ctx, PetService_AddPet_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) UpdatePet(ctx context.Context, in *Pet, opts ...grpc.CallOption) (*Pet, error) {
	out := new(Pet)
	err := c.cc.Invoke(ctx, PetService_UpdatePet_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Deprecated: Do not use.
func (c *petServiceClient) FindPetsByTag(ctx context.Context, in *TagReq, opts ...grpc.CallOption) (*Pets, error) {
	out := new(Pets)
	err := c.cc.Invoke(ctx, PetService_FindPetsByTag_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *petServiceClient) FindPetsByStatus(ctx context.Context, in *StatusReq, opts ...grpc.CallOption) (*Pets, error) {
	out := new(Pets)
	err := c.cc.Invoke(ctx, PetService_FindPetsByStatus_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PetServiceServer is the server API for PetService service.
// All implementations must embed UnimplementedPetServiceServer
// for forward compatibility
type PetServiceServer interface {
	GetPetByID(context.Context, *PetID) (*Pet, error)
	UpdatePetWithForm(context.Context, *UpdatePetWithFormReq) (*emptypb.Empty, error)
	DeletePet(context.Context, *PetID) (*emptypb.Empty, error)
	UploadFile(context.Context, *UploadFileReq) (*ApiResponse, error)
	AddPet(context.Context, *Pet) (*Pet, error)
	UpdatePet(context.Context, *Pet) (*Pet, error)
	// Deprecated: Do not use.
	FindPetsByTag(context.Context, *TagReq) (*Pets, error)
	FindPetsByStatus(context.Context, *StatusReq) (*Pets, error)
	mustEmbedUnimplementedPetServiceServer()
}

// UnimplementedPetServiceServer must be embedded to have forward compatible implementations.
type UnimplementedPetServiceServer struct {
}

func (UnimplementedPetServiceServer) GetPetByID(context.Context, *PetID) (*Pet, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetPetByID not implemented")
}
func (UnimplementedPetServiceServer) UpdatePetWithForm(context.Context, *UpdatePetWithFormReq) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdatePetWithForm not implemented")
}
func (UnimplementedPetServiceServer) DeletePet(context.Context, *PetID) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeletePet not implemented")
}
func (UnimplementedPetServiceServer) UploadFile(context.Context, *UploadFileReq) (*ApiResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UploadFile not implemented")
}
func (UnimplementedPetServiceServer) AddPet(context.Context, *Pet) (*Pet, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AddPet not implemented")
}
func (UnimplementedPetServiceServer) UpdatePet(context.Context, *Pet) (*Pet, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdatePet not implemented")
}
func (UnimplementedPetServiceServer) FindPetsByTag(context.Context, *TagReq) (*Pets, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindPetsByTag not implemented")
}
func (UnimplementedPetServiceServer) FindPetsByStatus(context.Context, *StatusReq) (*Pets, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindPetsByStatus not implemented")
}
func (UnimplementedPetServiceServer) mustEmbedUnimplementedPetServiceServer() {}

// UnsafePetServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to PetServiceServer will
// result in compilation errors.
type UnsafePetServiceServer interface {
	mustEmbedUnimplementedPetServiceServer()
}

func RegisterPetServiceServer(s grpc.ServiceRegistrar, srv PetServiceServer) {
	s.RegisterService(&PetService_ServiceDesc, srv)
}

func _PetService_GetPetByID_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PetID)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).GetPetByID(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_GetPetByID_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).GetPetByID(ctx, req.(*PetID))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_UpdatePetWithForm_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdatePetWithFormReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).UpdatePetWithForm(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_UpdatePetWithForm_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).UpdatePetWithForm(ctx, req.(*UpdatePetWithFormReq))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_DeletePet_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PetID)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).DeletePet(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_DeletePet_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).DeletePet(ctx, req.(*PetID))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_UploadFile_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UploadFileReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).UploadFile(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_UploadFile_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).UploadFile(ctx, req.(*UploadFileReq))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_AddPet_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Pet)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).AddPet(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_AddPet_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).AddPet(ctx, req.(*Pet))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_UpdatePet_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Pet)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).UpdatePet(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_UpdatePet_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).UpdatePet(ctx, req.(*Pet))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_FindPetsByTag_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TagReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).FindPetsByTag(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_FindPetsByTag_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).FindPetsByTag(ctx, req.(*TagReq))
	}
	return interceptor(ctx, in, info, handler)
}

func _PetService_FindPetsByStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StatusReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PetServiceServer).FindPetsByStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: PetService_FindPetsByStatus_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PetServiceServer).FindPetsByStatus(ctx, req.(*StatusReq))
	}
	return interceptor(ctx, in, info, handler)
}

// PetService_ServiceDesc is the grpc.ServiceDesc for PetService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var PetService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "io.swagger.petstore.v2.PetService",
	HandlerType: (*PetServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetPetByID",
			Handler:    _PetService_GetPetByID_Handler,
		},
		{
			MethodName: "UpdatePetWithForm",
			Handler:    _PetService_UpdatePetWithForm_Handler,
		},
		{
			MethodName: "DeletePet",
			Handler:    _PetService_DeletePet_Handler,
		},
		{
			MethodName: "UploadFile",
			Handler:    _PetService_UploadFile_Handler,
		},
		{
			MethodName: "AddPet",
			Handler:    _PetService_AddPet_Handler,
		},
		{
			MethodName: "UpdatePet",
			Handler:    _PetService_UpdatePet_Handler,
		},
		{
			MethodName: "FindPetsByTag",
			Handler:    _PetService_FindPetsByTag_Handler,
		},
		{
			MethodName: "FindPetsByStatus",
			Handler:    _PetService_FindPetsByStatus_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "io/swagger/petstore/v2/pets.proto",
}