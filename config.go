// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"

	xds "github.com/cncf/xds/go/xds/type/v3"
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
	"google.golang.org/protobuf/types/known/anypb"
)

type Config struct {
	echoBody string
	// other fields
}

type Parser struct{}

func (p *Parser) Parse(msg *anypb.Any) (interface{}, error) {
	configStruct := &xds.TypedStruct{}
	if err := msg.UnmarshalTo(configStruct); err != nil {
		return nil, err
	}

	v := configStruct.Value
	var conf Config
	prefix, ok := v.AsMap()["prefix_localreply_body"]
	if !ok {
		return nil, errors.New("missing prefix_localreply_body")
	}
	if str, ok := prefix.(string); ok {
		conf.echoBody = str
	} else {
		return nil, fmt.Errorf("prefix_localreply_body: expect string while got %T", prefix)
	}
	return &conf, nil
}

func (p *Parser) Merge(parent interface{}, child interface{}) interface{} {
	parentConfig, _ := parent.(*Config)
	childConfig, _ := child.(*Config)

	// copy one, do not update parentConfig directly.
	newConfig := *parentConfig
	if childConfig.echoBody != "" {
		newConfig.echoBody = childConfig.echoBody
	}
	return &newConfig
}

func ConfigFactory(c interface{}) api.StreamFilterFactory {
	conf := c.(*Config) //nolint:errcheck,forcetypeassert
	mux := newMux(conf)

	return func(callbacks api.FilterCallbackHandler) api.StreamFilter {
		return &filterEnvoy{
			mux: mux,

			callbacks: callbacks,
		}
	}
}
