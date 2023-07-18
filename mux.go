// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

type mux struct {
	config *Config
}

func newMux(config *Config) *mux {
	return &mux{
		config: config,
	}
}
