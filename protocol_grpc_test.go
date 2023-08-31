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
	"math"
	"net/http/httptest"
	"testing"
	"testing/quick"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Len(t, rec.Body.Bytes(), 0)

	got := grpcExtractErrorFromTrailer(rec.Header())
	assert.Equal(t, cerr, got)

	// Now again, but this time an error with details
	errDetail, err := connect.NewErrorDetail(&wrapperspb.StringValue{Value: "foo"})
	require.NoError(t, err)
	cerr.AddDetail(errDetail)
	rec = httptest.NewRecorder()
	grpcWriteEndToTrailers(&responseEnd{err: cerr}, rec.Header())

	assert.Equal(t, "16", rec.Header().Get("Grpc-Status"))
	assert.Equal(t, "test error: Hello, %E4%B8%96%E7%95%8C", rec.Header().Get("Grpc-Message"))
	assert.Equal(t, "CBASGXRlc3QgZXJyb3I6IEhlbGxvLCDkuJbnlYwaOAovdHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUucHJvdG9idWYuU3RyaW5nVmFsdWUSBQoDZm9v", rec.Header().Get("Grpc-Status-Details-Bin"))
	assert.Len(t, rec.Body.Bytes(), 0)

	got = grpcExtractErrorFromTrailer(rec.Header())
	assert.Equal(t, cerr, got)
}

func TestGRPCEncodeTimeoutQuick(t *testing.T) {
	t.Parallel()
	// Ensure that the error case is actually unreachable.
	encode := func(d time.Duration) bool {
		_, ok := grpcEncodeTimeout(d)
		return ok
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
		decoded := grpcPercentDecode(encoded)
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
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			assert.True(t, utf8.ValidString(input), "input invalid UTF-8")
			encoded := grpcPercentEncode(input)
			t.Logf("%q encoded as %q", input, encoded)
			decoded := grpcPercentDecode(encoded)
			assert.Equal(t, decoded, input)
		})
	}
}

func TestGRPCDecodeTimeout(t *testing.T) {
	t.Parallel()
	_, err := grpcDecodeTimeout("")
	assert.True(t, errors.Is(err, errNoTimeout))

	_, err = grpcDecodeTimeout("foo")
	assert.NotNil(t, err)
	_, err = grpcDecodeTimeout("12xS")
	assert.NotNil(t, err)
	_, err = grpcDecodeTimeout("999999999n") // 9 digits
	assert.NotNil(t, err)
	assert.False(t, errors.Is(err, errNoTimeout))
	_, err = grpcDecodeTimeout("99999999H") // 8 digits but overflows time.Duration
	assert.True(t, errors.Is(err, errNoTimeout))

	duration, err := grpcDecodeTimeout("45S")
	assert.Nil(t, err)
	assert.Equal(t, duration, 45*time.Second)

	const long = "99999999S"
	duration, err = grpcDecodeTimeout(long) // 8 digits, shouldn't overflow
	assert.Nil(t, err)
	assert.Equal(t, duration, 99999999*time.Second)
}

func TestGRPCEncodeTimeout(t *testing.T) {
	t.Parallel()
	timeout, ok := grpcEncodeTimeout(time.Hour + time.Second)
	assert.True(t, ok)
	assert.Equal(t, timeout, "3601000m")
	timeout, ok = grpcEncodeTimeout(time.Duration(math.MaxInt64))
	assert.True(t, ok)
	assert.Equal(t, timeout, "2562047H")
	timeout, ok = grpcEncodeTimeout(-1 * time.Hour)
	assert.True(t, ok)
	assert.Equal(t, timeout, "0n")
}
