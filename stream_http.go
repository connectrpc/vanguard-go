// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
)

type upstreamHTTPRule struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	params  params
	method  *method
	recv    int32
	send    int32
}

var _ upstream = (*upstreamHTTPRule)(nil)

func (u *upstreamHTTPRule) Protocol() protocol { return protocolHTTPRule }
func (u *upstreamHTTPRule) DecodeHeader(hdr requestHeader) error {
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
func (u *upstreamHTTPRule) DecodeMessage(b []byte, msg proto.Message) (err error) {
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
func (u *upstreamHTTPRule) EncodeHeader(hdr responseHeader) error {
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
func (u *upstreamHTTPRule) EncodeMessage(b []byte, msg proto.Message) ([]byte, error) {
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
func (u *upstreamHTTPRule) EncodeTrailer(hdr header) error {
	// TODO: clear map?
	return nil
}
func (u *upstreamHTTPRule) EncodeError(rsp io.Writer, hdr responseHeader, err error) {
	statusCode := http.StatusInternalServerError
	status := &status.Status{
		Code:    1, // internal
		Message: err.Error(),
	}
	if se := (*statusError)(nil); errors.As(err, &se) {
		statusCode = se.CodeHTTP
		status.Code = int32(se.CodeGRPC)
		status.Details = se.Details
	}

	hdr.WriteStatus(statusCode)
	fmt.Println("got status", status)

	b, err := u.codec.MarshalAppend(nil, status)
	if err != nil {
		panic(err)
	}
	_, _ = rsp.Write(b)
}

func encodeErrorHTTP(rsp http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	status := &status.Status{
		Code:    1, // internal
		Message: err.Error(),
	}
	if se := (*statusError)(nil); errors.As(err, &se) {
		statusCode = se.CodeHTTP
		status.Code = int32(se.CodeGRPC)
		status.Details = se.Details
	}

	rsp.WriteHeader(statusCode)
	fmt.Println("got status", status)

	b, err := json.Marshal(status)
	if err != nil {
		panic(err)
	}
	_, _ = rsp.Write(b)
}
