// Copyright 2023-2026 Buf Technologies, Inc.
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

// PetServiceClient is what the connect-go v2 code generator would emit
// for this service; we inline it here because we don't yet vendor a v2
// codegen for the test protos.

package main

import (
	"context"

	"connectrpc.com/connect/v2"
	v2 "connectrpc.com/vanguard/internal/gen/io/swagger/petstore/v2"
	"google.golang.org/protobuf/types/known/emptypb"
)

const PetServiceName = "io.swagger.petstore.v2.PetService"

const (
	PetServiceGetPetByIDProcedure       = "/io.swagger.petstore.v2.PetService/GetPetByID"
	PetServiceAddPetProcedure           = "/io.swagger.petstore.v2.PetService/AddPet"
	PetServiceUpdatePetProcedure        = "/io.swagger.petstore.v2.PetService/UpdatePet"
	PetServiceDeletePetProcedure        = "/io.swagger.petstore.v2.PetService/DeletePet"
	PetServiceFindPetsByStatusProcedure = "/io.swagger.petstore.v2.PetService/FindPetsByStatus"
)

var petServiceMethods = v2.File_io_swagger_petstore_v2_pets_proto.
	Services().
	ByName("PetService").
	Methods()

// PetServiceClient is a thin wrapper around a connect.Client that
// presents the RPC service as Go methods. The user holds onto a
// PetServiceClient and never touches HTTP details.
type PetServiceClient struct {
	client *connect.Client

	getPetByIDSpec       connect.Spec
	addPetSpec           connect.Spec
	updatePetSpec        connect.Spec
	deletePetSpec        connect.Spec
	findPetsByStatusSpec connect.Spec
}

func NewPetServiceClient(transport connect.Transport, interceptors ...connect.ClientInterceptor) *PetServiceClient {
	return &PetServiceClient{
		client: connect.NewClient(transport, interceptors...),
		getPetByIDSpec: connect.Spec{
			StreamType:       connect.StreamTypeUnary,
			Schema:           petServiceMethods.ByName("GetPetByID"),
			Procedure:        PetServiceGetPetByIDProcedure,
			IdempotencyLevel: connect.IdempotencyNoSideEffects,
		},
		addPetSpec: connect.Spec{
			StreamType: connect.StreamTypeUnary,
			Schema:     petServiceMethods.ByName("AddPet"),
			Procedure:  PetServiceAddPetProcedure,
		},
		updatePetSpec: connect.Spec{
			StreamType: connect.StreamTypeUnary,
			Schema:     petServiceMethods.ByName("UpdatePet"),
			Procedure:  PetServiceUpdatePetProcedure,
		},
		deletePetSpec: connect.Spec{
			StreamType: connect.StreamTypeUnary,
			Schema:     petServiceMethods.ByName("DeletePet"),
			Procedure:  PetServiceDeletePetProcedure,
		},
		findPetsByStatusSpec: connect.Spec{
			StreamType:       connect.StreamTypeUnary,
			Schema:           petServiceMethods.ByName("FindPetsByStatus"),
			Procedure:        PetServiceFindPetsByStatusProcedure,
			IdempotencyLevel: connect.IdempotencyNoSideEffects,
		},
	}
}

func (c *PetServiceClient) GetPetByID(ctx context.Context, req *v2.PetID) (*v2.Pet, error) {
	var res v2.Pet
	return &res, c.client.CallUnary(ctx, c.getPetByIDSpec, req, &res)
}

func (c *PetServiceClient) AddPet(ctx context.Context, req *v2.Pet) (*v2.Pet, error) {
	var res v2.Pet
	return &res, c.client.CallUnary(ctx, c.addPetSpec, req, &res)
}

func (c *PetServiceClient) UpdatePet(ctx context.Context, req *v2.Pet) (*v2.Pet, error) {
	var res v2.Pet
	return &res, c.client.CallUnary(ctx, c.updatePetSpec, req, &res)
}

func (c *PetServiceClient) DeletePet(ctx context.Context, req *v2.PetID) (*emptypb.Empty, error) {
	var res emptypb.Empty
	return &res, c.client.CallUnary(ctx, c.deletePetSpec, req, &res)
}

func (c *PetServiceClient) FindPetsByStatus(ctx context.Context, req *v2.StatusReq) (*v2.Pets, error) {
	var res v2.Pets
	return &res, c.client.CallUnary(ctx, c.findPetsByStatusSpec, req, &res)
}
