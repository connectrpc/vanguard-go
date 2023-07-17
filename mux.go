// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type mux struct {
	config      *Config
	codecs      map[string]codec
	compressors map[string]compressor

	mu    sync.Mutex // serialize updates to state
	state atomic.Pointer[state]
}

func newMux(config *Config) *mux {
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
	return &mux{
		config:      config,
		codecs:      codecs,
		compressors: compressors,
	}
}

func (m *mux) addService(sd protoreflect.ServiceDescriptor) error {
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

func (m *mux) getCodec(name string) (codec, error) {
	if c, ok := m.codecs[name]; ok {
		return c, nil
	}
	return nil, statusErrorf(http.StatusBadRequest, "codec %q doesn't exist", name)
}

func (m *mux) getCompressor(name string) (compressor, error) {
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
		return &state{}
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
