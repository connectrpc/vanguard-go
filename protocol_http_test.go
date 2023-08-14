// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestHTTPErrorWriter(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("test error: %s", "Hello, 世界")
	cerr := connect.NewWireError(connect.CodeUnauthenticated, err)
	rec := httptest.NewRecorder()
	httpWriteError(rec, cerr)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "identity", rec.Header().Get("Content-Encoding"))
	var out bytes.Buffer
	assert.NoError(t, json.Compact(&out, rec.Body.Bytes()))
	assert.Equal(t, `{"code":16,"message":"test error: Hello, 世界","details":[]}`, out.String())

	body := bytes.NewReader(rec.Body.Bytes())
	got := httpErrorFromResponse(body)
	assert.Equal(t, cerr, got)
}
