// Copyright 2023-2026 Buf Technologies, Inc.
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

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func asConnectError(err error) *connect.Error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		if connectErr.IsRemote() {
			// Don't forward a peer's RPC verdict as this handler's own.
			return connect.NewError(connect.CodeInternal, "").WithCause(err)
		}
		return connectErr
	}
	return connect.NewError(connect.CodeUnknown, "").WithCause(err)
}

// wrapError classifies err under code with a message prefix. An error that
// already carries a code is returned unchanged.
func wrapError(code connect.Code, prefix string, err error) *connect.Error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}
	return connect.Errorf(code, "%s: %s", prefix, err).WithCause(err)
}

func grpcStatusFromError(err *connect.Error) *status.Status {
	stat := &status.Status{
		Code:    int32(err.Code()), //nolint:gosec // No information loss.
		Message: err.Message(),
	}
	if details := err.Details(); len(details) > 0 {
		stat.Details = make([]*anypb.Any, len(details))
		for i, detail := range details {
			stat.Details[i] = &anypb.Any{
				TypeUrl: "type.googleapis.com/" + detail.Type,
				Value:   detail.Value,
			}
		}
	}
	return stat
}
