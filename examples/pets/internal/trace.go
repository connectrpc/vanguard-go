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

var id atomic.Int64

func TraceHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqId := id.Add(1)
		tr := &tracer{reqId: reqId}
		if r.RemoteAddr != "" {
			tr.traceReq("(from %s)", r.RemoteAddr)
		}
		traceRequest(tr, r)
		r.Body = &traceReader{r: r.Body, trace: tr.traceReq}
		r = r.WithContext(context.WithValue(r.Context(), traceRequestId{}, reqId))
		handler.ServeHTTP(&traceWriter{w: w, trace: tr.traceResp}, r)
	})
}

func TraceTransport(transport http.RoundTripper) http.RoundTripper {
	return traceTransport{transport}
}

type traceTransport struct {
	transport http.RoundTripper
}

type traceRequestId struct{}

func (t traceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	reqId, ok := request.Context().Value(traceRequestId{}).(int64)
	if !ok {
		reqId = id.Add(1)
	}
	tr := &tracer{reqId: reqId, prefix: "    "}
	traceRequest(tr, request)
	request.Body = &traceReader{r: request.Body, trace: tr.traceReq}
	resp, err := t.transport.RoundTrip(request)
	if err != nil {
		tr.traceResp("ERROR: %v", err)
		return nil, err
	}
	tr.traceResp("%s", resp.Status)
	traceHeaders(tr.traceResp, resp.Header)
	resp.Body = &traceReader{
		r:     resp.Body,
		trace: tr.traceResp,
		onEnd: func() {
			traceTrailers(tr.traceResp, resp.Trailer)
		},
	}
	return resp, nil
}

func traceRequest(tr *tracer, req *http.Request) {
	var queryString string
	if req.URL.RawQuery != "" {
		queryString = "?" + req.URL.RawQuery
	}
	tr.traceReq("%s http://%s%s%s %s", req.Method, req.Host, req.URL.Path, queryString, req.Proto)
	traceHeaders(tr.traceReq, req.Header)
	tr.traceReq("")
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
		if errors.Is(err, io.EOF) {
			t.trace("(EOF)")
			t.traceTrailers()
		} else {
			t.trace("(%v!)", err)
		}
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
	trailers := http.Header{}
	for k, v := range t.Header() {
		if strings.HasPrefix(k, http.TrailerPrefix) {
			trailers[k] = v
		}
	}
	for _, k := range t.trailersSnapshot {
		trailers[k] = t.Header().Values(k)
	}
	traceTrailers(t.trace, trailers)
}

func (t *traceWriter) Flush() {
	if flusher, ok := t.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

type tracer struct {
	reqId  int64
	prefix string
}

func (tr *tracer) traceReq(msg string, args ...interface{}) {
	fmt.Printf("%s#%04d>> %s\n", tr.prefix, tr.reqId, fmt.Sprintf(msg, args...))
}

func (tr *tracer) traceResp(msg string, args ...interface{}) {
	fmt.Printf("%s#%04d<< %s\n", tr.prefix, tr.reqId, fmt.Sprintf(msg, args...))
}
