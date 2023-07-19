// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"google.golang.org/genproto/googleapis/api/annotations"
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

func (m *Mux) addService(sd protoreflect.ServiceDescriptor) error {
	// Load the state for writing.
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state.Load().clone()

	if err := state.addService(sd); err != nil {
		return err
	}

	m.state.Store(state)
	return nil
}

func (m *Mux) getCodec(name string) (codec, error) {
	if c, ok := m.codecs[name]; ok {
		return c, nil
	}
	return nil, statusErrorf(http.StatusBadRequest, "codec %q doesn't exist", name)
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

func (m *Mux) getCompressor(name string) (compressor, error) {
	if c, ok := m.compressors[name]; ok {
		return c, nil
	}
	return nil, statusErrorf(http.StatusBadRequest, "compressor %q doesn't exist", name)
}

type state struct {
	path *path
	// TODO: canonical mapping of gRPC to REST method
	methodByName map[string]protoreflect.MethodDescriptor
}

func (s *state) getMethod(name string) (protoreflect.MethodDescriptor, error) {
	if m, ok := s.methodByName[name]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("method %q doesn't exist", name)
}

func (s *state) clone() *state {
	if s == nil {
		return &state{
			path: newPath(),
		}
	}
	methods := make(map[string]protoreflect.MethodDescriptor, len(s.methodByName))
	for k, v := range s.methodByName {
		methods[k] = v
	}
	return &state{
		path:         s.path.clone(),
		methodByName: methods,
	}
}

func (s *state) addService(sd protoreflect.ServiceDescriptor) error {
	mds := sd.Methods()
	for i := 0; i < mds.Len(); i++ {
		md := mds.Get(i)

		if err := s.addMethod(sd, md); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) addMethod(sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor) error {
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

	if s.methodByName == nil {
		s.methodByName = make(map[string]protoreflect.MethodDescriptor)
	}
	s.methodByName[name] = md
	return nil
}
