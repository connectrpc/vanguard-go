// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"google.golang.org/protobuf/reflect/protoreflect"
)

type methodConfig struct {
	descriptor protoreflect.MethodDescriptor
	// TODO: other config options
}
