// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type upstreamHTTP struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	params  params
	method  *method
	recv    int32
	send    int32
}

var _ upstream = (*upstreamHTTP)(nil)

func (u *upstreamHTTP) Protocol() protocol { return protocolHTTP }
func (u *upstreamHTTP) DecodeHeader(hdr requestHeader) error {
	contentType, ok := hdr.Get("Content-Type")
	if !ok {
		// Default to JSON
		contentType = "application/json"
		hdr.Set("Content-Type", contentType)
	}
	codec, err := u.getCodec(strings.TrimPrefix(contentType, "application/"))
	if err != nil {
		return err
	}
	u.codec = codec

	encoding, ok := hdr.Get("Content-Encoding")
	if ok && encoding != "identity" {
		comp, err := u.getCompressor(encoding)
		if err != nil {
			return err
		}
		u.decComp = comp
	}
	return nil
}
func (u *upstreamHTTP) DecodeMessage(b []byte, msg proto.Message) (err error) {
	if comp := u.decComp; comp != nil {
		b, err = u.decompress(b, comp)
		if err != nil {
			return err
		}
	}
	if len(b) > 0 {
		if err := u.codec.Unmarshal(b, msg); err != nil {
			return err
		}
	}
	if u.recv == 0 {
		if err := u.params.set(msg); err != nil {
			return err
		}
	}
	u.recv++
	return nil
}
func (u *upstreamHTTP) EncodeHeader(hdr responseHeader) error {
	encoding, _ := hdr.Get("Content-Encoding")
	if len(encoding) > 0 && encoding != "identity" {
		comp, err := u.getCompressor(encoding)
		if err != nil {
			return err
		}
		u.encComp = comp
	}
	return nil
}
func (u *upstreamHTTP) EncodeMessage(b []byte, msg proto.Message) ([]byte, error) {
	b, err := u.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	if comp := u.encComp; comp != nil {
		b, err = u.compress(b, comp)
		if err != nil {
			return nil, err
		}
	}
	u.send++
	return b, nil
}
func (u *upstreamHTTP) EncodeTrailer(hdr header) error {
	// TODO: clear map?
	return nil
}

type downstreamHTTP struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	params  params  // params taken from EncodeMessage
	method  *method // Method needs to be built
	recv    int32
	send    int32
}

var _ downstream = (*downstreamHTTP)(nil)

func (s *downstreamHTTP) EncodeHeader(requestHeader) error                    { return nil }
func (s *downstreamHTTP) EncodeMessage([]byte, proto.Message) ([]byte, error) { return nil, nil }
func (s *downstreamHTTP) DecodeHeader(responseHeader) error                   { return nil }
func (s *downstreamHTTP) DecodeMessage([]byte, proto.Message) error           { return nil }
func (s *downstreamHTTP) DecodeTrailer(header) error                          { return nil }

func newHTTPErrorWriter(contentType string) errorWriter {
	return func(body io.Writer, hdr responseHeader, err error) {
		var codec codec = codecJSON{
			MarshalOptions: protojson.MarshalOptions{
				EmitUnpopulated: true,
			},
		}
		if contentType == "application/protobuf" {
			codec = codecProto{}
		} else {
			contentType = "application/json"
		}

		cerr := asError(err)

		statusCode := rpcStatusCodeToHTTP(cerr.Code())
		if errors.Is(err, errMethodNotAllowed) {
			statusCode = http.StatusMethodNotAllowed
		}
		status := grpcStatusFromError(err)

		hdr.Set("Content-Type", contentType)
		hdr.Set("Content-Encoding", "identity")
		bin, err := codec.MarshalAppend(nil, status)
		if err != nil {
			statusCode = http.StatusInternalServerError
			hdr.Set("Content-Type", "application/json")
			bin = []byte(`{"code": 12, "message":"` + err.Error() + `"}`)
		}
		hdr.WriteStatus(statusCode)
		_, _ = body.Write(bin)
	}
}
