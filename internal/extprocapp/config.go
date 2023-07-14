// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package extprocapp

import (
	"crypto/tls"
	"os"
)

type ExternalConfig struct {
	H2CAddress string
	TLSAddress string
	TLSCert    string
	TLSKey     string
}

type Config struct {
	H2CAddress string
	TLSAddress string
	TLSConfig  *tls.Config
}

func NewConfig(externalConfig ExternalConfig) (*Config, error) {
	config := &Config{
		H2CAddress: externalConfig.H2CAddress,
		TLSAddress: externalConfig.TLSAddress,
		TLSConfig:  nil,
	}
	if externalConfig.TLSCert != "" && externalConfig.TLSKey != "" {
		cert, err := os.ReadFile(externalConfig.TLSCert)
		if err != nil {
			return nil, err
		}
		key, err := os.ReadFile(externalConfig.TLSKey)
		if err != nil {
			return nil, err
		}
		certificate, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, err
		}
		config.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
	}
	return config, nil
}
