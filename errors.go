// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
	headers func(header http.Header)
	err     error
}

func (e *httpError) Error() string {
	return e.err.Error()
}

func (e *httpError) Unwrap() error {
	return e.err
}

func httpCodeFromError(err error) (code int, headers func(header http.Header)) {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.code, httpErr.headers
	}
	var connErr *connect.Error
	if errors.As(err, &connErr) {
		return httpStatusCodeFromRPC(connErr.Code()), nil
	}
	return http.StatusInternalServerError, nil
}
