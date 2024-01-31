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

package vanguard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCanUseGlobalTypes(t *testing.T) {
	t.Parallel()
	t.Run("service descriptor from global files", func(t *testing.T) {
		t.Parallel()
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(testv1connect.LibraryServiceName)
		require.NoError(t, err)
		svcDesc, ok := desc.(protoreflect.ServiceDescriptor)
		require.True(t, ok)

		assert.True(t, canUseGlobalTypes(svcDesc))
	})
	t.Run("service descriptor not from global files", func(t *testing.T) {
		t.Parallel()
		prefix := randomPrefix(t)
		file, err := makeFile(prefix)
		require.NoError(t, err)

		svcDesc := file.Services().ByName("BlahService")
		require.NotNil(t, svcDesc)

		assert.False(t, canUseGlobalTypes(svcDesc))
	})
	t.Run("service descriptor from global files without types", func(t *testing.T) {
		t.Parallel()
		prefix := randomPrefix(t)
		file, err := makeFile(prefix)
		require.NoError(t, err)

		svcDesc := file.Services().ByName("BlahService")
		require.NotNil(t, svcDesc)

		err = protoregistry.GlobalFiles.RegisterFile(file)
		require.NoError(t, err)

		// Now file is in GlobalFiles, but the request and response types are NOT in GlobalTypes.
		// So this still returns false.
		assert.False(t, canUseGlobalTypes(svcDesc))
	})
}

func randomPrefix(t *testing.T) string {
	t.Helper()
	var prefixBytes [12]byte
	_, err := rand.Read(prefixBytes[:])
	require.NoError(t, err)
	return "x" + hex.EncodeToString(prefixBytes[:])
}

func makeFile(prefix string) (protoreflect.FileDescriptor, error) {
	pathPrefix := prefix
	if pathPrefix != "" {
		pathPrefix += "/"
	}
	pkgPrefix := prefix
	if pkgPrefix != "" {
		pkgPrefix += "."
	}
	var reg protoregistry.Files
	err := reg.RegisterFile((*timestamppb.Timestamp)(nil).ProtoReflect().Descriptor().ParentFile())
	if err != nil {
		return nil, fmt.Errorf("failed to register dependency: %w", err)
	}
	return protodesc.NewFile(
		&descriptorpb.FileDescriptorProto{
			Name:       proto.String(pathPrefix + "foo/bar/baz/v1/blah.proto"),
			Package:    proto.String(pkgPrefix + "foo.bar.baz.v1"),
			Dependency: []string{"google/protobuf/timestamp.proto"},
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("Blah"),
				},
				{
					Name: proto.String("Blarg"),
				},
				{
					Name: proto.String("Blech"),
				},
				{
					Name: proto.String("Bleep"),
				},
				{
					Name: proto.String("Blue"), // not actually used by service below
				},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("BlahService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						{
							Name:       proto.String("Do"),
							InputType:  proto.String("." + pkgPrefix + "foo.bar.baz.v1.Blah"),
							OutputType: proto.String("." + pkgPrefix + "foo.bar.baz.v1.Blarg"),
						},
						{
							Name:       proto.String("Dont"),
							InputType:  proto.String("." + pkgPrefix + "foo.bar.baz.v1.Blech"),
							OutputType: proto.String("." + pkgPrefix + "foo.bar.baz.v1.Bleep"),
						},
					},
				},
			},
		},
		&reg,
	)
}
