// Copyright 2023-2025 Buf Technologies, Inc.
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
	"math"
	"net/http/httptest"
	"testing"
	"testing/quick"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestGRPCErrorWriter(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("test error: %s", "Hello, 世界")
	cerr := connect.NewWireError(connect.CodeUnauthenticated, err)
	rec := httptest.NewRecorder()
	grpcWriteEndToTrailers(&responseEnd{err: cerr}, rec.Header())

	assert.Equal(t, "16", rec.Header().Get("Grpc-Status"))
	assert.Equal(t, "test error: Hello, %E4%B8%96%E7%95%8C", rec.Header().Get("Grpc-Message"))
	// if error has no details, no need to generate this response trailer
	assert.Equal(t, "", rec.Header().Get("Grpc-Status-Details-Bin"))
	assert.Empty(t, rec.Body.Bytes())

	got := grpcExtractErrorFromTrailer(rec.Header())
	compareErrors(t, cerr, got)

	// Now again, but this time an error with details
	errDetail, err := connect.NewErrorDetail(&wrapperspb.StringValue{Value: "foo"})
	require.NoError(t, err)
	cerr.AddDetail(errDetail)
	rec = httptest.NewRecorder()
	grpcWriteEndToTrailers(&responseEnd{err: cerr}, rec.Header())

	assert.Equal(t, "16", rec.Header().Get("Grpc-Status"))
	assert.Equal(t, "test error: Hello, %E4%B8%96%E7%95%8C", rec.Header().Get("Grpc-Message"))
	assert.Equal(t, "CBASGXRlc3QgZXJyb3I6IEhlbGxvLCDkuJbnlYwaOAovdHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUucHJvdG9idWYuU3RyaW5nVmFsdWUSBQoDZm9v", rec.Header().Get("Grpc-Status-Details-Bin"))
	assert.Empty(t, rec.Body.Bytes())

	got = grpcExtractErrorFromTrailer(rec.Header())
	compareErrors(t, cerr, got)
}

func TestGRPCEncodeTimeoutQuick(t *testing.T) {
	t.Parallel()
	// Ensure that the error case is actually unreachable.
	encode := func(d time.Duration) bool {
		v := grpcEncodeTimeout(d)
		return v != ""
	}
	if err := quick.Check(encode, nil); err != nil {
		t.Error(err)
	}
}

func TestGRPCPercentEncodingQuick(t *testing.T) {
	t.Parallel()
	roundtrip := func(input string) bool {
		if !utf8.ValidString(input) {
			return true
		}
		encoded := grpcPercentEncode(input)
		decoded, _ := grpcPercentDecode(encoded)
		return decoded == input
	}
	if err := quick.Check(roundtrip, nil /* config */); err != nil {
		t.Error(err)
	}
}

func TestGRPCPercentEncoding(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"foo",
		"foo bar",
		`foo%bar`,
		"fiancée",
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			assert.True(t, utf8.ValidString(input), "input invalid UTF-8")
			encoded := grpcPercentEncode(input)
			t.Logf("%q encoded as %q", input, encoded)
			decoded, _ := grpcPercentDecode(encoded)
			assert.Equal(t, decoded, input)
		})
	}
}

func TestGRPCDecodeTimeout(t *testing.T) {
	t.Parallel()
	_, err := grpcDecodeTimeout("")
	require.ErrorIs(t, err, errNoTimeout)

	_, err = grpcDecodeTimeout("foo")
	assert.Error(t, err) //nolint:testifylint
	_, err = grpcDecodeTimeout("12xS")
	assert.Error(t, err)                          //nolint:testifylint
	_, err = grpcDecodeTimeout("999999999n")      // 9 digits
	assert.Error(t, err)                          //nolint:testifylint
	assert.False(t, errors.Is(err, errNoTimeout)) //nolint:testifylint
	_, err = grpcDecodeTimeout("99999999H")       // 8 digits but overflows time.Duration
	assert.ErrorIs(t, err, errNoTimeout)          //nolint:testifylint

	duration, err := grpcDecodeTimeout("45S")
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, duration)

	const long = "99999999S"
	duration, err = grpcDecodeTimeout(long) // 8 digits, shouldn't overflow
	require.NoError(t, err)
	assert.Equal(t, 99999999*time.Second, duration)
}

func TestGRPCEncodeTimeout(t *testing.T) {
	t.Parallel()
	timeout := grpcEncodeTimeout(time.Hour + time.Second)
	assert.Equal(t, "3601000m", timeout)
	timeout = grpcEncodeTimeout(time.Duration(math.MaxInt64))
	assert.Equal(t, "2562047H", timeout)
	timeout = grpcEncodeTimeout(-1 * time.Hour)
	assert.Equal(t, "0n", timeout)
}

func compareErrors(t *testing.T, got, want *connect.Error) {
	t.Helper()
	assert.Equal(t, want.Code(), got.Code(), "wrong code")
	assert.Equal(t, want.Message(), got.Message(), "wrong message")
	wantDetails := want.Details()
	gotDetails := got.Details()
	if !assert.Len(t, wantDetails, len(gotDetails), "wrong number of details") {
		return
	}
	for i, wantDetail := range wantDetails {
		gotDetail := gotDetails[i]
		if assert.Equal(t, wantDetail.Type(), gotDetail.Type(), "wrong detail type at index %d", i) {
			wantedMsg, err := wantDetail.Value()
			require.NoError(t, err, "failed to deserialize wanted detail at index %d", i)
			gotMsg, err := gotDetail.Value()
			require.NoError(t, err, "failed to deserialize got detail at index %d", i)
			require.Empty(t, cmp.Diff(wantedMsg, gotMsg, protocmp.Transform()))
		}
	}
}
