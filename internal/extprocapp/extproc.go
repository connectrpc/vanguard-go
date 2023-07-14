// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package extprocapp

import (
	"github.com/bufbuild/vanguard/internal/extproc"
)

type ExternalProcessor struct{}

func NewExternalProcessor(config *Config) (extproc.ExternalProcessor, error) {
	return extproc.NewExternalProcessor(config.H2CAddress, config.TLSAddress, config.TLSConfig)
}
