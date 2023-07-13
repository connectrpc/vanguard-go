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
	"context"
	"flag"
)

func Main(arguments []string) error {
	flags := flag.NewFlagSet("extproc", flag.ContinueOnError)
	h2cAddress := flags.String("h2c-address", ":8080", "address to listen for h2c connections on")
	tlsAddress := flags.String("tls-address", "", "address to listen for tls connections on")
	tlsCertName := flags.String("tls-cert", "", "path to tls certificate")
	tlsKeyName := flags.String("tls-key", "", "path to tls private key")
	err := flags.Parse(arguments)
	if err != nil {
		return err
	}

	config, err := NewConfig(ExternalConfig{
		H2CAddress: *h2cAddress,
		TLSAddress: *tlsAddress,
		TLSCert:    *tlsCertName,
		TLSKey:     *tlsKeyName,
	})
	if err != nil {
		return err
	}

	app, err := NewExternalProcessor(config)
	if err != nil {
		return err
	}

	return app.Run(context.Background())
}
