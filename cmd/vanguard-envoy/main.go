// Copyright 2023 Buf Technologies, Inc.

package main

import (
	"github.com/bufbuild/vanguard"
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/http"
)

const Name = "vanguard"

func init() {
	http.RegisterHttpFilterConfigFactory(Name, vanguard.ConfigFactory)
	http.RegisterHttpFilterConfigParser(&vanguard.Parser{})
	//http.RegisterHttpFilterConfigFactoryAndParser(
	//	Name, vanguard.ConfigFactory, &vanguard.Parser{})
}

func main() {}
