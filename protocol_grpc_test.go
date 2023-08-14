// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
)

func TestGRPCErrorWriter(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("test error: %s", "Hello, 世界")
	cerr := connect.NewWireError(connect.CodeUnauthenticated, err)
	rec := httptest.NewRecorder()
	grpcWriteError(rec, cerr)

	assert.Equal(t, "identity", rec.Header().Get("Grpc-Encoding"))
	assert.Equal(t, "16", rec.Header().Get("Grpc-Status"))
	assert.Equal(t, "test error: Hello, %E4%B8%96%E7%95%8C", rec.Header().Get("Grpc-Message"))
	assert.Equal(t, "CBASGXRlc3QgZXJyb3I6IEhlbGxvLCDkuJbnlYw", rec.Header().Get("Grpc-Status-Details-Bin"))
	assert.Len(t, rec.Body.Bytes(), 0)

	got := grpcErrorFromTrailer(rec.Header())
	assert.Equal(t, cerr, got)
}

func TestGRPCEncodeTimeoutQuick(t *testing.T) {
	t.Parallel()
	// Ensure that the error case is actually unreachable.
	encode := func(d time.Duration) bool {
		_, err := grpcEncodeTimeout(d)
		return err == nil
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
	timeout, err := grpcEncodeTimeout(time.Hour + time.Second)
	assert.Nil(t, err)
	assert.Equal(t, timeout, "3601000m")
	timeout, err = grpcEncodeTimeout(time.Duration(math.MaxInt64))
	assert.Nil(t, err)
	assert.Equal(t, timeout, "2562047H")
	timeout, err = grpcEncodeTimeout(-1 * time.Hour)
	assert.Nil(t, err)
	assert.Equal(t, timeout, "0n")
}
