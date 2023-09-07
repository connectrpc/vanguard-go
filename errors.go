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

package vanguard

import (
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
)

func asConnectError(err error) *connect.Error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	return connect.NewError(connect.CodeInternal, err)
}

var errNoTimeout = errors.New("no timeout")

func errProtocol(msg string, args ...any) error {
	return fmt.Errorf("protocol error: "+msg, args...)
}

type httpError struct {
	code   int
	header http.Header
}

func (e *httpError) Error() string {
	return http.StatusText(e.code)
}
func (e *httpError) EncodeHeaders(header http.Header) {
	for key, vals := range e.header {
		for _, val := range vals {
			header.Add(key, val)
		}
	}
}
func (e *httpError) Encode(writer http.ResponseWriter) {
	e.EncodeHeaders(writer.Header())
	http.Error(writer, e.Error(), e.code)
}

func asHTTPError(err error) *httpError {
	if err == nil {
		return &httpError{code: http.StatusOK}
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
		}
	}
	return &httpError{code: http.StatusInternalServerError}
}

var errNotFound = &httpError{code: http.StatusNotFound}
