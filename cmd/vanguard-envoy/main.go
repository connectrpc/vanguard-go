// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package main

import (
	"github.com/bufbuild/vanguard"
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/http"
)

const Name = "vanguard"

func init() { //nolint:gochecknoinits
	http.RegisterHttpFilterConfigFactory(Name, vanguard.ConfigFactory)
	http.RegisterHttpFilterConfigParser(&vanguard.Parser{})
}

func main() {}
