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
