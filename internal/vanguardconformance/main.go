package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	"buf.build/gen/go/connectrpc/conformance/connectrpc/go/connectrpc/conformance/v1/conformancev1connect"
	conformancev1 "buf.build/gen/go/connectrpc/conformance/protocolbuffers/go/connectrpc/conformance/v1"
	"connectrpc.com/vanguard"
	"google.golang.org/protobuf/proto"
)

const (
	gracefulShutdownPeriod = 5 * time.Second
)

func getOrDefault(env, def string) string {
	if value := os.Getenv(env); value != "" {
		return value
	}
	return def
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stopSignalCtx := signal.NotifyContext(ctx, os.Interrupt)
	defer stopSignalCtx()
	if err := Run(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		log.Fatalf("an error occurred running the conformance server: %s", err.Error())
	}
}

// Run runs the conformance server as a proxy between the conformance test runner and the actual
// implementation of the reference server.
func Run(ctx context.Context, args []string, osStdin io.ReadCloser, osStdout, osStderr io.WriteCloser) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	var req conformancev1.ServerCompatRequest
	if err := decodeMessage(osStdin, &req); err != nil {
		return err
	}

	// Maybe mutate the input config here.
	var (
		stdin            bytes.Buffer
		stdoutR, stdoutW = io.Pipe()
		stdout           bytes.Buffer
		stderr           bytes.Buffer
		execDone         = make(chan error)
	)
	var tlsConfig *tls.Config
	var certBytes []byte
	if req.UseTls {
		creds := req.GetServerCreds()
		if creds == nil {
			return fmt.Errorf("missing server creds")
		}
		clientCertMode := tls.NoClientCert
		if len(req.ClientTlsCert) > 0 {
			clientCertMode = tls.RequireAndVerifyClientCert
		}
		var err error
		tlsConfig, err = newServerTLSConfig(
			creds.Cert, creds.Key, clientCertMode, req.ClientTlsCert,
		)
		if err != nil {
			return fmt.Errorf("failed to create server TLS config: %w", err)
		}
		certBytes = creds.Cert

		// Zero config.
		req.UseTls = false
		req.ServerCreds = nil
		req.ClientTlsCert = nil
	}
	if err := encodeMessage(&stdin, &req); err != nil {
		return err
	}

	// Create the reference server.
	cmd := exec.CommandContext(ctx, "go", "run", "connectrpc.com/conformance/cmd/referenceserver")
	cmd.Stdin = &stdin
	cmd.Stdout = io.MultiWriter(stdoutW, &stdout)
	cmd.Stderr = &stderr
	cmd.Cancel = func() error {
		err := cmd.Process.Signal(os.Interrupt)
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Printf("failed to interrupt process: %s", err)
			err = cmd.Process.Kill()
		}
		return err
	}
	cmd.WaitDelay = gracefulShutdownPeriod
	go func() {
		log.Println("Running conformance server")
		err := cmd.Run()
		execDone <- err
		log.Printf("Conformance server exit: %s\n", err)
	}()
	defer cmd.Cancel()

	var resp conformancev1.ServerCompatResponse
	if err := decodeMessage(stdoutR, &resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	select {
	case execErr := <-execDone:
		return fmt.Errorf("reference server exited early: %w", execErr)
	default:
	}

	refURL := &url.URL{
		Scheme: "http", // Always HTTP for the reference server.
		Host:   resp.Host + ":" + strconv.Itoa(int(resp.Port)),
	}

	// Create a server that proxies requests to the reference server.
	proxyHandler := httputil.NewSingleHostReverseProxy(refURL)

	opts := []vanguard.ServiceOption{
		vanguard.WithTargetProtocols(vanguard.ProtocolConnect),
		vanguard.WithTargetCodecs(vanguard.CodecJSON),
		vanguard.WithNoTargetCompression(),
	}
	services := []*vanguard.Service{
		vanguard.NewService(
			conformancev1connect.ConformanceServiceName,
			proxyHandler,
			opts...,
		),
	}
	handler, err := vanguard.NewTranscoder(services)
	if err != nil {
		return err
	}
	logHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("Serving", r.Method, r.URL)
		handler.ServeHTTP(w, r)
	})

	svr := &http.Server{
		Addr:      "127.0.0.1:0",
		Handler:   logHandler,
		TLSConfig: tlsConfig,
	}
	var l net.Listener
	if tlsConfig == nil {
		l, err = net.Listen("tcp", svr.Addr)
	} else {
		l, err = tls.Listen("tcp", svr.Addr, tlsConfig)
	}
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	go func() {
		if err := svr.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("proxy server error: %s\n", err)
		}
	}()
	defer func() {
		log.Println("Shutting down proxy server")
		ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownPeriod)
		defer cancel()
		if err := svr.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown proxy server: %s\n", err)
		}
	}()

	host, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return fmt.Errorf("failed to parse address: %s", err)
	}
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("failed to parse port: %s", port)
	}
	resp.Host = host
	resp.Port = uint32(portInt)
	resp.PemCert = certBytes

	if err := encodeMessage(osStdout, &resp); err != nil {
		return fmt.Errorf("failed to encode response: %w", err)
	}

	log.Printf("Serving conformance reference server on %s\n", refURL)
	log.Printf("Serving proxy server on %s\n", l.Addr())
	select {
	case execErr := <-execDone:
		log.Println("Conformance server exit")
		return execErr
	case <-ctx.Done():
		log.Println("Context done", ctx.Err())
		<-execDone
		return ctx.Err()
	}
}

func decodeMessage(input io.Reader, msg proto.Message) error {
	var head [4]byte
	if _, err := io.ReadFull(input, head[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(head[:])
	buf, err := io.ReadAll(io.LimitReader(input, int64(size)))
	if err != nil {
		return err
	}
	return proto.Unmarshal(buf, msg)
}

func encodeMessage(output io.Writer, msg proto.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(b)))
	if _, err := output.Write(head[:]); err != nil {
		return err
	}
	if _, err = output.Write(b); err != nil {
		return err
	}
	return nil
}

func newServerTLSConfig(
	cert, key []byte,
	clientCertMode tls.ClientAuthType,
	clientCACert []byte,
) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	if clientCertMode != tls.NoClientCert && len(clientCACert) == 0 {
		return nil, fmt.Errorf("clientCertMode indicates client certs supported but CACert is empty")
	}
	var caCertPool *x509.CertPool
	if len(clientCACert) > 0 {
		caCertPool = x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(clientCACert) {
			return nil, fmt.Errorf("failed to parse client CA cert from given data")
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    caCertPool,
		ClientAuth:   clientCertMode,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
