// Copyright 2023 Buf Technologies, Inc.

package vanguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	xds "github.com/cncf/xds/go/xds/type/v3"
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"

	//"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
	"google.golang.org/protobuf/types/known/anypb"
)

type Config struct {
	echoBody string
	// other fields

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

	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(5 * time.Second)
			fmt.Println("hello from config factory")
			rsp, err := http.Get("http://localhost:8080")
			if err != nil {
				fmt.Println(err)
			} else {
				b, _ := io.ReadAll(rsp.Body)
				fmt.Println(string(b))
			}
		}
	}()

	return func(callbacks api.FilterCallbackHandler) api.StreamFilter {
		return &filter{
			callbacks: callbacks,
			config:    conf,
		}
	}
}
