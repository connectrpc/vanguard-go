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

// This program calls the live Swagger Petstore service at
// https://petstore.swagger.io/v2/ using a Connect-shaped RPC client.
// vanguard sits behind the transport and translates each RPC into the
// REST request described by the method's google.api.http annotation.
//
// Run: go run ./internal/examples/petstore
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/vanguard"
	v2 "connectrpc.com/vanguard/internal/gen/io/swagger/petstore/v2"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := run(ctx)
	cancel()
	if err != nil {
		log.Fatalf("petstore demo: %v", err)
	}
}

func run(ctx context.Context) error {
	transport, err := vanguard.NewTransport(
		&http.Client{Timeout: 15 * time.Second},
		"https://petstore.swagger.io/v2",
	)
	if err != nil {
		return fmt.Errorf("build transport: %w", err)
	}
	client := NewPetServiceClient(transport)

	// Find a handful of available pets. FindPetsByStatus is GET-shaped
	// with status=available as a query parameter.
	list, err := client.FindPetsByStatus(ctx, &v2.StatusReq{Status: []string{v2.Status_available.String()}})
	if err != nil {
		return fmt.Errorf("FindPetsByStatus: %w", err)
	}
	fmt.Printf("found %d available pet(s)\n", len(list.GetPets()))
	for i, pet := range list.GetPets() {
		if i >= 3 {
			break
		}
		fmt.Printf("  - id=%d name=%q status=%s\n", pet.GetId(), pet.GetName(), pet.GetStatus())
	}

	// Add a new pet. AddPet is POST /pet with body "*".
	added, err := client.AddPet(ctx, &v2.Pet{
		Name:   "vanguard-demo-pet",
		Status: v2.Status_available.String(),
	})
	if err != nil {
		if cerr, ok := errors.AsType[*connect.Error](err); ok {
			return fmt.Errorf("AddPet failed (code=%s): %w", cerr.Code(), err)
		}
		return fmt.Errorf("AddPet: %w", err)
	}
	fmt.Printf("added pet id=%d\n", added.GetId())

	if added.GetId() == 0 {
		// Petstore demo backend often returns id=0; skip the rest.
		return nil
	}

	// Fetch the pet by ID. GetPetByID is GET /pet/{pet_id}.
	got, err := client.GetPetByID(ctx, &v2.PetID{PetId: added.GetId()})
	if err != nil {
		// The Petstore demo backend often drops just-added pets so a
		// 404 here is expected and informative rather than fatal.
		var cerr *connect.Error
		if errors.As(err, &cerr) && cerr.Code() == connect.CodeNotFound {
			fmt.Printf("GetPetByID: pet %d not found (expected on the demo backend)\n", added.GetId())
			return nil
		}
		return fmt.Errorf("GetPetByID: %w", err)
	}
	fmt.Printf("fetched pet id=%d name=%q\n", got.GetId(), got.GetName())
	return nil
}
