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
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TypeResolver can resolve message and extension types and is used to instantiate
// messages as needed for the middleware to serialize/de-serialize request and
// response payloads.
//
// Implementations of this interface should be comparable, so they can be used as
// map keys. Typical implementations are pointers to structs, which are suitable.
type TypeResolver interface {
	protoregistry.MessageTypeResolver
	protoregistry.ExtensionTypeResolver
}

type fallbackResolver []TypeResolver

func (f fallbackResolver) FindMessageByName(message protoreflect.FullName) (protoreflect.MessageType, error) {
	var lastErr error
	for _, res := range f {
		msgType, err := res.FindMessageByName(message)
		if err == nil {
			return msgType, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, protoregistry.NotFound
	}
	return nil, lastErr
}

func (f fallbackResolver) FindMessageByURL(url string) (protoreflect.MessageType, error) {
	var lastErr error
	for _, res := range f {
		msgType, err := res.FindMessageByURL(url)
		if err == nil {
			return msgType, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, protoregistry.NotFound
	}
	return nil, lastErr
}

func (f fallbackResolver) FindExtensionByName(field protoreflect.FullName) (protoreflect.ExtensionType, error) {
	var lastErr error
	for _, res := range f {
		extType, err := res.FindExtensionByName(field)
		if err == nil {
			return extType, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, protoregistry.NotFound
	}
	return nil, lastErr
}

func (f fallbackResolver) FindExtensionByNumber(message protoreflect.FullName, field protoreflect.FieldNumber) (protoreflect.ExtensionType, error) {
	var lastErr error
	for _, res := range f {
		extType, err := res.FindExtensionByNumber(message, field)
		if err == nil {
			return extType, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, protoregistry.NotFound
	}
	return nil, lastErr
}

func resolverForService(svcDesc protoreflect.ServiceDescriptor) (TypeResolver, error) {
	file := svcDesc.ParentFile()
	if file != nil {
		return resolverForFile(file)
	}

	// Unexpected that file is nil, but technically possible since ParentFile() is
	// specified as an optional operation that may return nil. So we'll create a
	// type registry of just the request and response types in the service.
	var reg protoregistry.Types
	methods := svcDesc.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		_, err := reg.FindMessageByName(methodDesc.Input().FullName())
		if err != nil {
			// not present so add it
			if err := reg.RegisterMessage(dynamicpb.NewMessageType(methodDesc.Input())); err != nil {
				return nil, err
			}
		}
		_, err = reg.FindMessageByName(methodDesc.Output().FullName())
		if err != nil {
			// not present so add it
			if err := reg.RegisterMessage(dynamicpb.NewMessageType(methodDesc.Output())); err != nil {
				return nil, err
			}
		}
	}
	return &reg, nil
}

func resolverForFile(file protoreflect.FileDescriptor) (TypeResolver, error) {
	var files protoregistry.Files
	if err := addFileRecursive(file, &files); err != nil {
		return nil, err
	}
	return dynamicpb.NewTypes(&files), nil
}

func addFileRecursive(file protoreflect.FileDescriptor, files *protoregistry.Files) error {
	if _, err := files.FindFileByPath(file.Path()); err == nil {
		// already registered
		return nil
	}
	if err := files.RegisterFile(file); err != nil {
		return err
	}
	imports := file.Imports()
	for i, length := 0, imports.Len(); i < length; i++ {
		depFile := imports.Get(i).FileDescriptor
		if err := addFileRecursive(depFile, files); err != nil {
			return err
		}
	}
	return nil
}

func canUseGlobalTypes(svcDesc protoreflect.ServiceDescriptor) bool {
	file := svcDesc.ParentFile()
	if file == nil {
		return false
	}
	registeredFile, err := protoregistry.GlobalFiles.FindFileByPath(file.Path())
	if err != nil || registeredFile != file {
		return false
	}
	// It is possible for code to register files in the global registry but fail to
	// register corresponding types in protoregistry.GlobalTypes. So before we return
	// true, make sure that all of the service's request and response messages can
	// actually be satisfied by the global types registry.
	methods := svcDesc.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		if _, err := protoregistry.GlobalTypes.FindMessageByName(methodDesc.Input().FullName()); err != nil {
			return false
		}
		if _, err := protoregistry.GlobalTypes.FindMessageByName(methodDesc.Output().FullName()); err != nil {
			return false
		}
	}
	return true
}
