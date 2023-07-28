// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/bufbuild/connect-go"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Mux struct {
	config      *Config
	codecs      map[string]codec
	compressors map[string]compressor
	buffers     bufferPool

	mu    sync.Mutex // serialize updates to state
	state atomic.Pointer[state]
}

func NewMux(config *Config) *Mux {
	codecs := map[string]codec{
		"proto": codecProto{},
		"json": codecJSON{
			MarshalOptions: protojson.MarshalOptions{
				EmitUnpopulated: true,
			},
		},
	}
	compressors := map[string]compressor{
		"gzip": &compressorGzip{},
	}
	return &Mux{
		config:      config,
		codecs:      codecs,
		compressors: compressors,
	}
}

type handleFunc func(
	http.ResponseWriter, *http.Request, protocol, *method, params,
) error

// ServeHTTP implements http.Handler.
func (m *Mux) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	reqHdr := makeRequestHeaderHTTP(req)
	rspHdr := makeResponseHeaderHTTP(rsp)

	upstreamProtocol, errorWriter := classifyProtocol(reqHdr)
	if upstreamProtocol == protocolUnknown {
		errorWriter(rsp, rspHdr, errUnsupportedProtocol(upstreamProtocol))
		return
	}

	lexer := lexer{input: req.URL.Path}
	if err := lexPath(&lexer); err != nil {
		errorWriter(rsp, rspHdr, err)
		return
	}
	toks := lexer.tokens()

	state := m.state.Load()
	if state == nil {
		err := errorf(connect.CodeInternal, "mux not initialized")
		errorWriter(rsp, rspHdr, err)
		return
	}
	verb := req.Method
	method, params, err := state.path.search(toks, verb) // GRPC, GRPCWeb, HTTPRule
	if err != nil {
		errorWriter(rsp, rspHdr, err)
		return
	}
	switch upstreamProtocol {
	case protocolGRPC:
		// TODO: validate request?
		if req.ProtoMajor != 2 {
			errorWriter(rsp, rspHdr, fmt.Errorf("unsupported protocol version: %d", req.ProtoMajor))
			return
		}
	case protocolGRPCWeb:
		panic("TODO")
	case protocolHTTP:
		queryParams, err := method.parseQueryParams(req.URL.Query())
		if err != nil {
			errorWriter(rsp, rspHdr, err)
			return
		}
		params = append(params, queryParams...)
	default:
		panic("TODO")
	}
	hd, err := state.pickMethodHandler(method.name)
	if err != nil {
		errorWriter(rsp, rspHdr, err)
		return
	}
	if err := hd(rsp, req, upstreamProtocol, method, params); err != nil {
		errorWriter(rsp, rspHdr, err)
		return
	}
}

func (m *Mux) getCodec(name string) (codec, error) {
	if c, ok := m.codecs[name]; ok {
		return c, nil
	}
	return nil, errorf(connect.CodeInternal, "unknown codec %q", name)
}

func (m *Mux) compress(b []byte, comp compressor) ([]byte, error) {
	buffer := m.buffers.Get()
	defer m.buffers.Put(buffer)

	wc, err := comp.Compress(buffer)
	if err != nil {
		return nil, err
	}
	if _, err := wc.Write(b); err != nil {
		return nil, err
	}
	if err := wc.Close(); err != nil {
		return nil, err
	}
	return append(b[:0], buffer.Bytes()...), nil

}
func (m *Mux) decompress(b []byte, comp compressor) ([]byte, error) {
	buffer := m.buffers.Get()
	defer m.buffers.Put(buffer)

	src := bytes.NewReader(b)
	rc, err := comp.Decompress(src)
	if err != nil {
		return nil, err
	}
	if _, err := buffer.ReadFrom(rc); err != nil {
		return nil, err
	}
	return append(b[:0], buffer.Bytes()...), nil
}

// getCompressor returns the compressor for the given name.
// https://github.com/grpc/grpc/blob/master/doc/compression.md
func (m *Mux) getCompressor(name string) (compressor, error) {
	if c, ok := m.compressors[name]; ok {
		return c, nil
	}
	return nil, errorf(connect.CodeUnimplemented, "unknown compressor %q", name)
}

type state struct {
	path *path

	handlers map[string][]handleFunc // method name to handlers
}

func (s *state) clone() *state {
	if s == nil {
		return &state{
			path:     newPath(),
			handlers: make(map[string][]handleFunc),
		}
	}
	handlers := make(map[string][]handleFunc)
	for method, hds := range s.handlers {
		handlers[method] = hds // shallow copy
	}
	return &state{
		path:     s.path.clone(),
		handlers: make(map[string][]handleFunc),
	}
}

func (s *state) addService(sd protoreflect.ServiceDescriptor, hd handleFunc) error {
	mds := sd.Methods()
	for i := 0; i < mds.Len(); i++ {
		md := mds.Get(i)

		if err := s.addMethod(sd, md, hd); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) addMethod(sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor, hd handleFunc) error {
	name := "/" + string(sd.FullName()) + "/" + string(md.Name())

	// Add an implicit rule for the method.
	implicitRule := &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Custom{
			Custom: &annotations.CustomHttpPattern{
				Kind: "*",
				Path: name,
			},
		},
		Body: "*",
	}
	if err := s.path.addRule(implicitRule, md, name); err != nil {
		panic(fmt.Sprintf("bug: %v", err))
	}

	// Add all annotated rules.
	if rule := getExtensionHTTP(md.Options()); rule != nil {
		if err := s.path.addRule(rule, md, name); err != nil {
			return fmt.Errorf("[%s] invalid rule %s: %w", md.FullName(), rule.String(), err)
		}
	}

	// Add the handler.
	s.handlers[name] = append(s.handlers[name], hd)
	return nil
}

func (s *state) pickMethodHandler(name string) (handleFunc, error) {
	if s != nil {
		hds := s.handlers[name]
		if len(hds) > 0 {
			hd := hds[rand.Intn(len(hds))]
			return hd, nil
		}
	}
	return nil, status.Errorf(codes.Unimplemented, "method %s not implemented", name)

}
