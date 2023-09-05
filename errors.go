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
	code    int
	headers func(http.Header)
}

func (e *httpError) Error() string {
	return http.StatusText(e.code)
}
func (e *httpError) Code() int {
	return e.code
}
func (e *httpError) Write(w http.ResponseWriter) {
	if e.headers != nil {
		e.headers(w.Header())
	}
	http.Error(w, e.Error(), e.code)
}
func (e *httpError) Headers() func(http.Header) {
	return e.headers
}

func asHTTPError(err error) *httpError {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return &httpError{
			code: httpStatusCodeFromRPC(ce.Code()),
			headers: func(h http.Header) {
				for key, vals := range ce.Meta() {
					for _, val := range vals {
						h.Add(key, val)
					}
				}
			},
		}
	}
	return &httpError{
		code: http.StatusInternalServerError,
	}
}
