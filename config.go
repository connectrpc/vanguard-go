// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"

	_ "github.com/bufbuild/vanguard/internal/gen/library/v1"

	xds "github.com/cncf/xds/go/xds/type/v3"
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
	//"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

type Config struct {
	echoBody string
	// other fields

	outputProtocol protocol
}

type Parser struct{}

func (p *Parser) Parse(any *anypb.Any) (interface{}, error) {
	configStruct := &xds.TypedStruct{}
	if err := any.UnmarshalTo(configStruct); err != nil {
		return nil, err
	}

	v := configStruct.Value
	conf := &Config{}
	prefix, ok := v.AsMap()["prefix_localreply_body"]
	if !ok {
		return nil, errors.New("missing prefix_localreply_body")
	}
	if str, ok := prefix.(string); ok {
		conf.echoBody = str
	} else {
		return nil, fmt.Errorf("prefix_localreply_body: expect string while got %T", prefix)
	}
	conf.outputProtocol = protocolGRPC // TODO
	return conf, nil
}

func (p *Parser) Merge(parent interface{}, child interface{}) interface{} {
	parentConfig := parent.(*Config)
	childConfig := child.(*Config)

	// copy one, do not update parentConfig directly.
	newConfig := *parentConfig
	if childConfig.echoBody != "" {
		newConfig.echoBody = childConfig.echoBody
	}
	return &newConfig
}

func ConfigFactory(c interface{}) api.StreamFilterFactory {
	conf, ok := c.(*Config)
	if !ok {
		panic("unexpected config type")
	}

	mux := newMux(conf)
	fmt.Println("adding services")
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		sds := fd.Services()
		for i := 0; i < sds.Len(); i++ {
			sd := sds.Get(i)
			fmt.Println(sd.FullName())
			mux.addService(sd)
			return true
		}
		return true
	})
	fmt.Println("done adding services")

	return func(callbacks api.FilterCallbackHandler) api.StreamFilter {
		return &filterEnvoy{
			mux: mux,

			callbacks: callbacks,
		}
	}
}
