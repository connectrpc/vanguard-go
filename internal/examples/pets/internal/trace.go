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

package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

//nolint:gochecknoglobals
var idSource atomic.Int64

func TraceHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		reqID := idSource.Add(1)
		trc := &tracer{reqID: reqID}
		if request.RemoteAddr != "" {
			trc.traceReq("(from %s)", request.RemoteAddr)
		}
		traceRequest(trc, request)
		request.Body = &traceReader{r: request.Body, trace: trc.traceReq}
		request = request.WithContext(context.WithValue(request.Context(), traceRequestID{}, reqID))
		tw := &traceWriter{w: responseWriter, trace: trc.traceResp}
		defer tw.traceTrailers()
		handler.ServeHTTP(tw, request)
	})
}

func TraceTransport(transport http.RoundTripper) http.RoundTripper {
	return traceTransport{transport}
}

type traceTransport struct {
	transport http.RoundTripper
}

type traceRequestID struct{}

func (t traceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	reqID, ok := request.Context().Value(traceRequestID{}).(int64)
	if !ok {
		reqID = idSource.Add(1)
	}
	trc := &tracer{reqID: reqID, prefix: "    "}
	traceRequest(trc, request)
	request.Body = &traceReader{r: request.Body, trace: trc.traceReq}
	resp, err := t.transport.RoundTrip(request)
	if err != nil {
		trc.traceResp("ERROR: %v", err)
		return nil, err
	}
	trc.traceResp("%s", resp.Status)
	traceHeaders(trc.traceResp, resp.Header)
	resp.Body = &traceReader{
		r:     resp.Body,
		trace: trc.traceResp,
		onEnd: func() {
			traceTrailers(trc.traceResp, resp.Trailer)
		},
	}
	return resp, nil
}

func traceRequest(trc *tracer, req *http.Request) {
	var queryString string
	if req.URL.RawQuery != "" {
		queryString = "?" + req.URL.RawQuery
	}
	scheme := req.URL.Scheme
	if scheme == "" {
		scheme = "http"
	}
	trc.traceReq("%s %s://%s%s%s %s", req.Method, scheme, req.Host, req.URL.Path, queryString, req.Proto)
	traceHeaders(trc.traceReq, req.Header)
	trc.traceReq("")
}

func traceHeaders(trace func(string, ...any), header http.Header) {
	for k, v := range header {
		for _, val := range v {
			trace("%s: %s", k, val)
		}
	}
}

func traceTrailers(trace func(string, ...any), trailer http.Header) {
	if len(trailer) == 0 {
		return
	}
	trace("")
	traceHeaders(trace, trailer)
}

type traceReader struct {
	r     io.ReadCloser
	trace func(string, ...any)
	done  bool
	onEnd func()
}

func (t *traceReader) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if n > 0 {
		t.trace("(%d bytes)", n)
	}
	if err != nil && !t.done {
		t.done = true
		if errors.Is(err, io.EOF) {
			t.trace("(EOF)")
			if t.onEnd != nil {
				t.onEnd()
			}
		} else {
			t.trace("(%v!)", err)
		}
	}
	return n, err
}

func (t *traceReader) Close() error {
	return t.r.Close()
}

type traceWriter struct {
	w                http.ResponseWriter
	trace            func(string, ...any)
	wroteHeaders     bool
	trailersSnapshot []string
	done             bool
}

func (t *traceWriter) Header() http.Header {
	return t.w.Header()
}

func (t *traceWriter) Write(bytes []byte) (n int, err error) {
	if !t.wroteHeaders {
		t.WriteHeader(http.StatusOK)
	}
	n, err = t.w.Write(bytes)
	if n > 0 {
		t.trace("(%d bytes)", n)
	}
	if err != nil && !t.done {
		t.done = true
		t.trace("(%v!)", err)
	}
	return n, err
}

func (t *traceWriter) WriteHeader(statusCode int) {
	if t.wroteHeaders {
		return
	}
	t.wroteHeaders = true
	trailers := t.Header().Values("Trailer")
	t.trailersSnapshot = make([]string, 0, len(trailers))
	for _, trailer := range trailers {
		for _, k := range strings.Split(trailer, ",") {
			t.trailersSnapshot = append(t.trailersSnapshot, strings.TrimSpace(k))
		}
	}
	t.w.WriteHeader(statusCode)
	t.trace("%d %s", statusCode, http.StatusText(statusCode))
	traceHeaders(t.trace, t.Header())
	t.trace("")
}

func (t *traceWriter) traceTrailers() {
	if t.done {
		return
	}
	trailers := http.Header{}
	for k, v := range t.Header() {
		if strings.HasPrefix(k, http.TrailerPrefix) {
			trailers[strings.TrimPrefix(k, http.TrailerPrefix)] = v
		}
	}
	for _, k := range t.trailersSnapshot {
		vals := t.Header().Values(k)
		if len(vals) > 0 {
			trailers[k] = vals
		}
	}
	traceTrailers(t.trace, trailers)
}

func (t *traceWriter) Flush() {
	if flusher, ok := t.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

type tracer struct {
	reqID  int64
	prefix string
}

func (trc *tracer) traceReq(msg string, args ...interface{}) {
	fmt.Printf("%s#%04d>> %s\n", trc.prefix, trc.reqID, fmt.Sprintf(msg, args...))
}

func (trc *tracer) traceResp(msg string, args ...interface{}) {
	fmt.Printf("%s#%04d<< %s\n", trc.prefix, trc.reqID, fmt.Sprintf(msg, args...))
}
