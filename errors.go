// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	connect "github.com/bufbuild/connect-go"
)

type errorWriter func(io.Writer, responseHeader, error)

func rpcStatusCodeToHTTP(c connect.Code) int {
	var codes = [...]int{
		http.StatusOK,                  // 0 OK
		http.StatusRequestTimeout,      // 1 Canceled
		http.StatusInternalServerError, // 2 Unknown
		http.StatusBadRequest,          // 3 InvalidArgument
		http.StatusGatewayTimeout,      // 4 DeadlineExceeded
		http.StatusNotFound,            // 5 NotFound
		http.StatusConflict,            // 6 AlreadyExists
		http.StatusForbidden,           // 7 PermissionDenied
		http.StatusTooManyRequests,     // 8 ResourceExhausted
		http.StatusBadRequest,          // 9 FailedPrecondition
		http.StatusConflict,            // 10 Aborted
		http.StatusBadRequest,          // 11 OutOfRange
		http.StatusNotImplemented,      // 12 Unimplemented
		http.StatusInternalServerError, // 13 Internal
		http.StatusServiceUnavailable,  // 14 Unavailable
		http.StatusInternalServerError, // 15 DataLoss
		http.StatusUnauthorized,        // 16 Unauthenticated
	}
	if int(c) > len(codes) {
		return http.StatusInternalServerError
	}
	return codes[c]
}

func errorf(code connect.Code, msg string, args ...any) *connect.Error {
	return connect.NewError(code, fmt.Errorf(msg, args...))
}

func errUnsupportedProtocol(p protocol) *connect.Error {
	return errorf(connect.CodeUnimplemented, "unsupported protocol: %s", p)
}
func errDecompressorNotFound(name string) *connect.Error {
	return errorf(connect.CodeInternal, "missing decompressor: %q", name)
}
func errUnsupportedProtocolConversion(up, down protocol) *connect.Error {
	return errorf(connect.CodeUnimplemented,
		"unsupported protocol conversion: %s to %s", up, down)
}

func asError(err error) *connect.Error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	return connect.NewError(connect.CodeInternal, err)
}
