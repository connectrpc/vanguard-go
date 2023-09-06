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
	"strings"

	"connectrpc.com/connect"
)

func asConnectError(err error) *connect.Error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	code := connect.CodeInternal
	if errors.Is(err, notFoundError{}) {
		code = connect.CodeUnimplemented
	} else if errors.Is(err, methodNotAllowedError{}) {
		code = connect.CodeUnimplemented
	}
	return connect.NewError(code, err)
}

var errNoTimeout = errors.New("no timeout")

func errProtocol(msg string, args ...any) error {
	return fmt.Errorf("protocol error: "+msg, args...)
}

type notFoundError struct{}

func (e notFoundError) Error() string {
	return http.StatusText(http.StatusNotFound)
}

type methodNotAllowedError struct {
	method  string
	allowed []string
}

func (e methodNotAllowedError) Error() string {
	return http.StatusText(http.StatusMethodNotAllowed)
}
func (e methodNotAllowedError) EncodeHeader(h http.Header) {
	h.Add("Allow", strings.Join(e.allowed, ","))
}
