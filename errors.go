// Copyright 2024 Buf Technologies, Inc.
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

package vanguard

import (
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
)

var (
	errNoTimeout = errors.New("no timeout")
	errNotFound  = &httpError{code: http.StatusNotFound}
)

func asConnectError(err error) *connect.Error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	return connect.NewError(connect.CodeInternal, err)
}

type httpError struct {
	code   int
	header http.Header
	err    error
}

func newHTTPError(statusCode int, msgFormat string, args ...any) *httpError {
	return &httpError{
		code: statusCode,
		err:  fmt.Errorf(msgFormat, args...),
	}
}

func (e *httpError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return http.StatusText(e.code)
}

func (e *httpError) Unwrap() error {
	return e.err
}

func (e *httpError) EncodeHeaders(header http.Header) {
	if e == nil {
		return
	}
	for key, vals := range e.header {
		for _, val := range vals {
			header.Add(key, val)
		}
	}
}

func (e *httpError) Encode(writer http.ResponseWriter) {
	if e == nil {
		writer.WriteHeader(http.StatusOK)
		return
	}
	e.EncodeHeaders(writer.Header())
	http.Error(writer, e.Error(), e.code)
}

func asHTTPError(err error) *httpError {
	if err == nil {
		return nil
	}
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return &httpError{
			code:   httpStatusCodeFromRPC(ce.Code()),
			header: ce.Meta(),
			err:    err,
		}
	}
	return &httpError{code: http.StatusInternalServerError, err: err}
}

func protocolError(msg string, args ...any) error {
	return fmt.Errorf("protocol error: "+msg, args...)
}

func bufferLimitError(limit int64) error {
	return sizeLimitError("max buffer size", limit)
}

func contentLengthError(limit int64) error {
	return sizeLimitError("content length", limit)
}

func sizeLimitError(what string, limit int64) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("%s (%d) exceeded", what, limit))
}

func malformedRequestError(err error) error {
	// Adds 400 Bad Request / InvalidArgument status codes to error
	return connect.NewError(connect.CodeInvalidArgument, err)
}
