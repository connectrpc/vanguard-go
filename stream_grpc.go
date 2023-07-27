// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type upstreamGRPC struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	isWeb   bool
}

var _ upstream = (*upstreamGRPC)(nil)

func (s *upstreamGRPC) Protocol() protocol                   { return protocolGRPC }
func (s *upstreamGRPC) DecodeHeader(hdr requestHeader) error { return nil }
func (s *upstreamGRPC) DecodeMessage([]byte, proto.Message) error { // bytes -> msg
	return nil
}
func (s *upstreamGRPC) EncodeHeader(hdr responseHeader) error {
	return nil
}
func (s *upstreamGRPC) EncodeMessage([]byte, proto.Message) ([]byte, error) { // msg -> bytes
	return nil, nil
}
func (s *upstreamGRPC) EncodeTrailer(hdr header) error {
	return nil
}
func (s *upstreamGRPC) EncodeError(rsp io.Writer, hdr responseHeader, err error) {}

type downstreamGRPC struct {
	*Mux

	methodDesc protoreflect.MethodDescriptor

	codec   codec
	encComp compressor // recv
	decComp compressor // send
	isWeb   bool
}

var _ downstream = (*downstreamGRPC)(nil)

func (s *downstreamGRPC) Protocol() protocol { return protocolGRPC }

func (s *downstreamGRPC) EncodeHeader(hdr requestHeader) error {
	// Convert protocol to gRPC
	hdr.SetProto("HTTP/2", 2, 0)
	hdr.SetMethod(http.MethodPost)

	serviceDesc := s.methodDesc.Parent().(protoreflect.ServiceDescriptor)
	name := "/" + string(serviceDesc.FullName()) +
		"/" + string(s.methodDesc.Name())
	u := hdr.URL()
	u.Path = name
	u.RawPath = name
	u.RawQuery = ""

	contentType, ok := hdr.Get("Content-Type")
	if !ok {
		// Default to JSON
		contentType = "application/json"
		hdr.Set("Content-Type", contentType)
	}
	_, codecName, ok := strings.Cut(contentType, "+")
	if !ok {
		codecName = "proto"
	}
	codec, err := s.getCodec(codecName)
	if err != nil {
		return err
	}
	s.codec = codec

	hdr.Set("Grpc-Accept-Encoding", "identity")

	// TODO: encoding...
	return nil
}
func (d *downstreamGRPC) EncodeMessage(b []byte, msg proto.Message) ([]byte, error) { // msg -> bytes
	b = append(b, 0, 0, 0, 0, 0)
	b, err := d.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	// TODO: minCompressSize
	if comp := d.encComp; comp != nil && len(b) > 0 {
		c, err := d.compress(b[5:], comp)
		if err != nil {
			return nil, err
		}
		b[0] |= 0x01
		b = append(b[:5], c...)
	}
	binary.BigEndian.PutUint32(b[1:5], uint32(len(b)-5))
	return b, nil
}
func (d *downstreamGRPC) DecodeHeader(hdr responseHeader) error {
	fmt.Println("response header", hdr)
	return nil
}
func (d *downstreamGRPC) DecodeMessage(b []byte, msg proto.Message) (err error) {
	// TODO: flags..
	isCompressed := b[0]&0x01 == 1
	if isCompressed {
		comp := d.decComp
		if comp == nil {
			return errDecompressorNotFound()
		}
		c, err := d.decompress(b[5:], comp)
		if err != nil {
			return err
		}
		b = c
	} else {
		b = b[5:] // TODO: check len
	}
	return d.codec.Unmarshal(b, msg)
}
func (d *downstreamGRPC) DecodeTrailer(hdr header) error {
	fmt.Println("response trailer", hdr)
	return nil
}
