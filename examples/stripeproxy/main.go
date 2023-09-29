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

package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"connectrpc.com/vanguard"
	_ "connectrpc.com/vanguard/internal/gen/stripe/v1"
)

func main() {
	flagset := flag.NewFlagSet("stripeproxy", flag.ExitOnError)
	port := flagset.String("p", "8080", "port to serve on")
	addr := flagset.String("url", "https://api.stripe.com", "base URL to proxy to")
	debug := flagset.Bool("debug", false, "enable debug logging")
	if err := flagset.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	remote, err := url.Parse(*addr)
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(remote)
	if *debug {
		proxy.Transport = &DebugTransport{}
	}
	// Create the handler for the proxy.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println(r.Method, r.URL)
		r.Host = remote.Host
		w.Header().Set("X-Vanguard", "StripeProxy")
		proxy.ServeHTTP(w, r)
	})

	mux := &vanguard.Mux{
		Protocols: []vanguard.Protocol{
			// Convert all requests to REST.
			vanguard.ProtocolREST,
		},
	}
	// Register the Stripe PaymentIntentsService.
	if err := mux.RegisterServiceByName(handler, "stripe.v1.PaymentIntentsService"); err != nil {
		log.Fatal(err)
	}

	// Alter the codec to use proto names for JSON.
	mux.AddCodec(vanguard.CodecJSON, func(resolver vanguard.TypeResolver) vanguard.Codec {
		codec := vanguard.DefaultJSONCodec(resolver)
		codec.MarshalOptions.UseProtoNames = true
		return codec
	})

	log.Printf("Proxy %s on HTTP port: %s\n", *addr, *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}

type DebugTransport struct{}

func (d *DebugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, err := httputil.DumpRequest(req, true)
	if err != nil {
		return nil, err
	}
	log.Println("Request:", string(raw))
	log.Println(">", req.URL, "Host:", req.Host)
	rsp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err = httputil.DumpResponse(rsp, true)
	if err != nil {
		return nil, err
	}
	log.Println("Response:", string(raw))
	return rsp, nil
}
