// Copyright 2023 Buf Technologies, Inc.
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
		config.TLSConfig = new(tls.Config)
		config.TLSConfig.MinVersion = tls.VersionTLS12
		config.TLSConfig.Certificates = []tls.Certificate{certificate}
	}
	return config, nil
}
