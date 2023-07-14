// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"
)

var codeToHTTPStatus = [...]int{
	http.StatusOK,                  // 0
	http.StatusRequestTimeout,      // 1
	http.StatusInternalServerError, // 2
	http.StatusBadRequest,          // 3
	http.StatusGatewayTimeout,      // 4
	http.StatusNotFound,            // 5
	http.StatusConflict,            // 6
	http.StatusForbidden,           // 7
	http.StatusTooManyRequests,     // 8
	http.StatusBadRequest,          // 9
	http.StatusConflict,            // 10
	http.StatusBadRequest,          // 11
	http.StatusNotImplemented,      // 12
	http.StatusInternalServerError, // 13
	http.StatusServiceUnavailable,  // 14
	http.StatusInternalServerError, // 15
	http.StatusUnauthorized,        // 16
}

func statusCodeToHTTP(c int) int {
	if int(c) > len(codeToHTTPStatus) {
		return http.StatusInternalServerError
	}
	return codeToHTTPStatus[c]
}
