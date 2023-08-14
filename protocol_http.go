// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func httpStatusCodeFromRPC(code connect.Code) int {
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
	if int(code) > len(codes) {
		return http.StatusInternalServerError
	}
	return codes[code]
}

func httpWriteError(rsp http.ResponseWriter, err error) {
	codec := protojson.MarshalOptions{
		EmitUnpopulated: true,
	}
	cerr := asError(err)
	statusCode := httpStatusCodeFromRPC(cerr.Code())
	status := grpcStatusFromError(err)

	hdr := rsp.Header()
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Content-Encoding", "identity")
	bin, err := codec.MarshalAppend(nil, status)
	if err != nil {
		statusCode = http.StatusInternalServerError
		hdr.Set("Content-Type", "application/json")
		bin = []byte(`{"code": 12, "message":"` + err.Error() + `"}`)
	}
	rsp.WriteHeader(statusCode)
	_, _ = rsp.Write(bin)
}

func httpErrorFromResponse(body io.Reader) *connect.Error {
	codec := protojson.UnmarshalOptions{}
	body = io.LimitReader(body, 1024)
	bin, err := io.ReadAll(body)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	var status status.Status
	if err := codec.Unmarshal(bin, &status); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	connectErr := connect.NewWireError(
		connect.Code(status.Code),
		errors.New(status.Message),
	)
	for _, msg := range status.Details {
		errDetail, _ := connect.NewErrorDetail(msg)
		connectErr.AddDetail(errDetail)
	}
	return connectErr
}
