// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"buf.build/gen/go/connectrpc/eliza/connectrpc/go/connectrpc/eliza/v1/elizav1connect"
	"buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	testDataString           = "abc def ghi"
	testCompressedDataString = "nop qrs tuv" // rot13 of above
)

func TestHandler_Errors(t *testing.T) {
	t.Parallel()
	// These tests exercise error-handling in the way the operation is initialized.
	// These tests should not reach the underlying handler or any particular protocol
	// handler implementation (other than extracting request metadata).

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	})
	grpcMux := &Mux{Protocols: []Protocol{ProtocolGRPC, ProtocolGRPCWeb}}
	connectMux := &Mux{Protocols: []Protocol{ProtocolConnect}}
	allMux := &Mux{} // supports all three
	for _, mux := range []*Mux{grpcMux, connectMux, allMux} {
		err := mux.RegisterServiceByName(handler, elizav1connect.ElizaServiceName)
		require.NoError(t, err)
	}

	// We use a handful of servers w/ different config.
	server := httptest.NewUnstartedServer(allMux.AsHandler())
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	grpcServer := httptest.NewUnstartedServer(grpcMux.AsHandler())
	grpcServer.EnableHTTP2 = true
	grpcServer.StartTLS()
	t.Cleanup(grpcServer.Close)
	connectServer := httptest.NewUnstartedServer(connectMux.AsHandler())
	connectServer.EnableHTTP2 = true
	connectServer.StartTLS()
	t.Cleanup(connectServer.Close)
	http1Server := httptest.NewServer(allMux.AsHandler())
	t.Cleanup(http1Server.Close)

	testCases := []struct {
		name                    string
		server                  *httptest.Server // default to server if unspecified
		requestURL              string
		requestMethod           string
		requestHeaders          map[string][]string
		expectedCode            int
		expectedResponseHeaders map[string]string
	}{
		{
			name:          "multiple content types",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/proto", "application/json"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "no content type, looks like connect from header",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "DELETE",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "no content type, looks like connect from query string",
			requestURL:    "/service.Foo/Bar?connect=v1",
			requestMethod: "DELETE",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "rest, route not found",
			requestURL:    "/foo/bar/baz",
			requestMethod: "PUT",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/json"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect stream, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect post, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect get, method not found",
			requestURL:    "/service.Foo/Bar?connect=v1",
			requestMethod: "GET",
			expectedCode:  http.StatusNotFound,
		},
		{
			name:          "grpc, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "grpc-web, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect get, method not idempotent",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say?connect=v1",
			requestMethod: "GET",
			expectedCode:  http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect post, bad HTTP method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "DELETE",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect stream, bad HTTP method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "grpc, bad HTTP method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "PUT",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "grpc-web, bad HTTP method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "PATCH",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect stream, unknown codec",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown codec",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown codec",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown codec",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unknown compression, pass-through",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":             {"application/connect+proto"},
				"Connect-Content-Encoding": {"blah"},
			},
			// When a supported protocol and codec, middleware will pass through
			// with unsupported compression and let underlying handler complain.
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "connect stream, unknown compression",
			server:        grpcServer, // must target different protocol for the error
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":             {"application/connect+proto"},
				"Connect-Content-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown compression, pass-through",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
				"Content-Encoding":         {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "connect post, unknown compression",
			server:        grpcServer,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
				"Content-Encoding":         {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown compression, pass-through",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "grpc, unknown compression",
			server:        connectServer,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown compression, pass-through",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc-web+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "grpc-web, unknown compression",
			server:        connectServer,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc-web+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, bidi and http 1.1",
			server:        http1Server,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc stream, bidi and http 1.1",
			server:        http1Server,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc-web stream, bidi and http 1.1",
			server:        http1Server,
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "connect post, stream method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Converse",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unary method",
			requestURL:    "/connectrpc.eliza.v1.ElizaService/Say",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		// TODO: add more tests around connect GET when we have a test
		//       proto to use other than eliza and a method that can
		//       actually support GET.
		// TODO: add more tests around REST when we have a test proto
		//       to use other than eliza and methods with http
		//       annotations.

	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			targetServer := server
			if testCase.server != nil {
				targetServer = testCase.server
			}
			req, err := http.NewRequestWithContext(context.Background(), testCase.requestMethod, targetServer.URL+testCase.requestURL, http.NoBody)
			require.NoError(t, err)
			for k, vals := range testCase.requestHeaders {
				for _, v := range vals {
					req.Header.Add(k, v)
				}
			}
			resp, err := targetServer.Client().Do(req)
			require.NoError(t, err)
			err = resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, testCase.expectedCode, resp.StatusCode)
			for k, v := range testCase.expectedResponseHeaders {
				require.Equal(t, v, resp.Header.Get(k))
			}
		})
	}
}

func TestHandler_PassThrough(t *testing.T) {
	t.Parallel()
	// These cases don't do any transformation and just pass through to the
	// underlying handler.

	// TODO: Use library service that combinatorial tests will use? Maybe use upcoming
	//       test interceptor to set up the scenarios instead of handler impl?
	_, impl := elizav1connect.NewElizaServiceHandler(&elizaHandler{})

	const testCaseIDKey = "Test-Case-Id"
	var testCaseID atomic.Int32
	var testCaseMap sync.Map
	checkPassThrough := http.HandlerFunc(func(respWriter http.ResponseWriter, request *http.Request) {
		// Get a *testing.T for this request so we can attribute error to correct case.
		testID := request.Header.Get(testCaseIDKey)
		if testID == "" {
			http.Error(respWriter, "request did not include test case ID", http.StatusBadRequest)
			return
		}
		val, ok := testCaseMap.Load(testID)
		if !ok {
			http.Error(respWriter, fmt.Sprintf("test case ID %q not found", testID), http.StatusBadRequest)
			return
		}
		t, ok := val.(*testing.T)
		if !ok {
			http.Error(respWriter, fmt.Sprintf("test case ID %q has unexpected type: %T", testID, val), http.StatusBadRequest)
			return
		}

		_, isWrapped := respWriter.(*responseWriter)
		require.False(t, isWrapped)
		_, isWrapped = request.Body.(*envelopingReader)
		require.False(t, isWrapped)
		_, isWrapped = request.Body.(*transformingReader)
		require.False(t, isWrapped)

		// carry on...
		impl.ServeHTTP(respWriter, request)
	})

	var mux Mux
	err := mux.RegisterServiceByName(checkPassThrough, elizav1connect.ElizaServiceName)
	require.NoError(t, err)

	// Use HTTP/2 so we can test a bidi stream.
	server := httptest.NewUnstartedServer(mux.AsHandler())
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	type connectClientCase struct {
		name string
		opts []connect.ClientOption
	}
	compressionOptions := []connectClientCase{
		{
			name: "identity",
		},
		{
			name: "gzip",
			opts: []connect.ClientOption{connect.WithSendCompression(CompressionGzip)},
		},
	}
	encodingOptions := []connectClientCase{
		{
			name: "proto",
			opts: []connect.ClientOption{connect.WithCodec(protoConnectCodec{})},
		},
		{
			name: "json",
			opts: []connect.ClientOption{connect.WithCodec(jsonConnectCodec{})},
		},
	}
	protocolOptions := []connectClientCase{
		{
			name: "connect",
		},
		{
			name: "grpc",
			opts: []connect.ClientOption{connect.WithGRPC()},
		},
		{
			name: "grpc-web",
			opts: []connect.ClientOption{connect.WithGRPCWeb()},
		},
	}

	for _, protocolCase := range protocolOptions {
		protocolCase := protocolCase
		t.Run(protocolCase.name, func(t *testing.T) {
			t.Parallel()
			for _, encodingCase := range encodingOptions {
				encodingCase := encodingCase
				t.Run(encodingCase.name, func(t *testing.T) {
					t.Parallel()
					for _, compressionCase := range compressionOptions {
						compressionCase := compressionCase
						t.Run(compressionCase.name, func(t *testing.T) {
							t.Parallel()

							testID := strconv.Itoa(int(testCaseID.Add(1)))
							testCaseMap.Store(testID, t)

							clientOptions := make([]connect.ClientOption, 0, 4)
							clientOptions = append(clientOptions, protocolCase.opts...)
							clientOptions = append(clientOptions, encodingCase.opts...)
							clientOptions = append(clientOptions, compressionCase.opts...)
							clientOptions = append(clientOptions, connect.WithInterceptors(
								addHeaderClientInterceptor{name: testCaseIDKey, value: testID},
							))
							client := elizav1connect.NewElizaServiceClient(server.Client(), server.URL, clientOptions...)

							// Unary cases
							resp, err := client.Say(context.Background(), connect.NewRequest(&elizav1.SayRequest{
								Sentence: "I feel happy today",
							}))
							require.NoError(t, err)
							require.NotEmpty(t, resp.Msg.Sentence)

							_, err = client.Say(context.Background(), connect.NewRequest(&elizav1.SayRequest{
								Sentence: "error:the vibe is in shambles:resource_exhausted",
							}))
							require.ErrorContains(t, err, "the vibe is in shambles")
							require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

							// Stream
							str, err := client.Introduce(context.Background(), connect.NewRequest(&elizav1.IntroduceRequest{
								Name: "Bob Loblaw",
							}))
							require.NoError(t, err)
							var count int
							for str.Receive() {
								count++
								require.NotEmpty(t, str.Msg().Sentence)
							}
							require.NoError(t, str.Err())
							require.Equal(t, 3, count)

							// Bidi stream
							bidi := client.Converse(context.Background())
							defer func() {
								err := bidi.CloseResponse()
								require.NoError(t, err)
							}()
							bidi.RequestHeader().Set(testCaseIDKey, testID)
							for i := 0; i < 10; i++ {
								sentence := strings.Repeat("foo,", i)
								err := bidi.Send(&elizav1.ConverseRequest{
									Sentence: sentence,
								})
								require.NoError(t, err)
								resp, err := bidi.Receive()
								require.NoError(t, err)
								require.Contains(t, resp.Sentence, sentence)
							}
							err = bidi.CloseRequest()
							require.NoError(t, err)
							_, err = bidi.Receive()
							require.ErrorIs(t, err, io.EOF)
						})
					}
				})
			}
		})
	}
}

func TestMessage_AdvanceStage(t *testing.T) {
	t.Parallel()
	// Tests the state machine for message.

	type testEnviron struct {
		abcCodec, xyzCodec                               *fakeCodec
		abcCompression, xyzCompression, otherCompression *fakeCompression
		op                                               *operation
	}
	newTestEnviron := func(isRequest bool) *testEnviron {
		abcCodec := &fakeCodec{name: "abc"}
		xyzCodec := &fakeCodec{name: "xyz"}
		abcCompression := &fakeCompression{name: "abc"}
		xyzCompression := &fakeCompression{name: "xyz"}
		otherCompression := &fakeCompression{name: "other"}
		var clientCodec, serverCodec Codec
		var clientReqComp, serverReqComp, respComp *compressionPool
		if isRequest {
			clientCodec = abcCodec
			serverCodec = xyzCodec
			clientReqComp = abcCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = otherCompression.newPool()
		} else {
			clientCodec = xyzCodec
			serverCodec = abcCodec
			clientReqComp = xyzCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = abcCompression.newPool()
		}
		op := &operation{
			bufferPool: newBufferPool(),
			client: clientProtocolDetails{
				codec:          clientCodec,
				reqCompression: clientReqComp,
			},
			server: serverProtocolDetails{
				codec:          serverCodec,
				reqCompression: serverReqComp,
			},
			respCompression: respComp,
		}
		return &testEnviron{
			abcCodec:         abcCodec,
			xyzCodec:         xyzCodec,
			abcCompression:   abcCompression,
			xyzCompression:   xyzCompression,
			otherCompression: otherCompression,
			op:               op,
		}
	}
	resetEnv := func(env *testEnviron) {
		env.abcCodec.marshalCalls = 0
		env.abcCodec.unmarshalCalls = 0
		env.xyzCodec.marshalCalls = 0
		env.xyzCodec.unmarshalCalls = 0
		env.abcCompression.compressorCalls = 0
		env.abcCompression.decompressorCalls = 0
		env.xyzCompression.compressorCalls = 0
		env.xyzCompression.decompressorCalls = 0
		env.otherCompression.compressorCalls = 0
		env.otherCompression.decompressorCalls = 0
	}
	type expectedCounts struct {
		abcMarshalCalls    int
		abcUnmarshalCalls  int
		xyzMarshalCalls    int
		xyzUnmarshalCalls  int
		abcCompressCalls   int
		abcDecompressCalls int
		xyzCompressCalls   int
		xyzDecompressCalls int
	}
	checkCounts := func(t *testing.T, isRequest bool, env *testEnviron, counts expectedCounts) {
		t.Helper()
		if !isRequest {
			// for responses, compression for both client and server is the same (abc)
			counts.abcCompressCalls += counts.xyzCompressCalls
			counts.abcDecompressCalls += counts.xyzDecompressCalls
			counts.xyzCompressCalls = 0
			counts.xyzDecompressCalls = 0
		}
		assert.Equal(t, counts.abcMarshalCalls, env.abcCodec.marshalCalls)
		assert.Equal(t, counts.abcUnmarshalCalls, env.abcCodec.unmarshalCalls)
		assert.Equal(t, counts.xyzMarshalCalls, env.xyzCodec.marshalCalls)
		assert.Equal(t, counts.xyzUnmarshalCalls, env.xyzCodec.unmarshalCalls)
		assert.Equal(t, counts.abcCompressCalls, env.abcCompression.compressorCalls)
		assert.Equal(t, counts.abcDecompressCalls, env.abcCompression.decompressorCalls)
		assert.Equal(t, counts.xyzCompressCalls, env.xyzCompression.compressorCalls)
		assert.Equal(t, counts.xyzDecompressCalls, env.xyzCompression.decompressorCalls)
		assert.Zero(t, env.otherCompression.compressorCalls)
		assert.Zero(t, env.otherCompression.decompressorCalls)
	}

	testCases := []struct {
		name                      string
		createMessage             func() *message
		decodedToSend             expectedCounts
		decodedToSendIfCompressed *expectedCounts
		readToSend                expectedCounts
		readToSendIfCompressed    *expectedCounts
	}{
		{
			name:          "same codec, same compression",
			createMessage: func() *message { return &message{sameCodec: true, sameCompression: true} },
			// no calls necessary since client payload can be re-used
			decodedToSend: expectedCounts{},
			readToSend:    expectedCounts{},
		},
		{
			name:          "same codec, different compression",
			createMessage: func() *message { return &message{sameCodec: true} },
			// no calls necessary for uncompressed since payload can be re-used,
			// but we have to decompress/recompress for compressed payloads
			decodedToSend: expectedCounts{},
			decodedToSendIfCompressed: &expectedCounts{
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				xyzCompressCalls:   1,
			},
		},
		{
			name:          "different codec",
			createMessage: func() *message { return &message{} },
			// we must re-encode and re-compress
			decodedToSend: expectedCounts{
				xyzMarshalCalls: 1,
			},
			decodedToSendIfCompressed: &expectedCounts{
				xyzMarshalCalls:  1,
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{
				abcUnmarshalCalls: 1,
				xyzMarshalCalls:   1,
			},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				abcUnmarshalCalls:  1,
				xyzMarshalCalls:    1,
				xyzCompressCalls:   1,
			},
		},
	}

	for _, compressed := range []bool{true, false} {
		compressed := compressed
		t.Run(fmt.Sprintf("compressed:%v", compressed), func(t *testing.T) {
			t.Parallel()
			for _, isRequest := range []bool{true, false} {
				isRequest := isRequest
				t.Run(fmt.Sprintf("request:%v", isRequest), func(t *testing.T) {
					t.Parallel()
					for _, testCase := range testCases {
						testCase := testCase
						t.Run(testCase.name, func(t *testing.T) {
							t.Parallel()

							originalData := testDataString
							if compressed {
								originalData = testCompressedDataString
							}

							env := newTestEnviron(isRequest)
							msg := testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer := msg.reset(env.op.bufferPool, isRequest, compressed)
							checkStageEmpty(t, msg, compressed)

							buffer.WriteString(originalData)
							msg.stage = stageRead
							checkStageRead(t, msg, compressed)

							err := msg.advanceToStage(env.op, stageDecoded)
							require.NoError(t, err)
							// read -> decoded must always decode (and possibly first decompress)
							counts := expectedCounts{
								abcUnmarshalCalls: 1,
							}
							if compressed {
								counts.abcDecompressCalls = 1
							}
							checkCounts(t, isRequest, env, counts)
							checkStageDecoded(t, msg)

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.decodedToSend
							if compressed && testCase.decodedToSendIfCompressed != nil {
								counts = *testCase.decodedToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)

							// Re-create message and this time go directly from read to send
							msg = testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer = msg.reset(env.op.bufferPool, isRequest, compressed)
							buffer.WriteString(originalData)
							msg.stage = stageRead

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.readToSend
							if compressed && testCase.readToSendIfCompressed != nil {
								counts = *testCase.readToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)
						})
					}
				})
			}
		})
	}
}

func TestIntersection(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		a, b, result []string
		resultCap    int
	}{
		{
			name:      "b is superset",
			a:         []string{"a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:      "a is superset",
			a:         []string{"a", "b", "c", "d", "e", "f"},
			b:         []string{"a", "b", "c"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:   "a is empty",
			a:      nil,
			b:      []string{"a", "b", "c", "d", "e", "f"},
			result: nil,
		},
		{
			name:   "b is empty",
			a:      []string{"a", "b", "c"},
			b:      nil,
			result: nil,
		},
		{
			name:      "result is empty",
			a:         []string{"a", "b", "c"},
			b:         []string{"d", "e", "f"},
			result:    []string{}, // only nil when one of the inputs is empty
			resultCap: 3,
		},
		{
			name:      "result is subset of both",
			a:         []string{"x", "y", "z", "a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 6,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := intersect(testCase.a, testCase.b)
			require.Equal(t, testCase.result, result)
			require.Equal(t, testCase.resultCap, cap(result))
		})
	}
}

func checkStageEmpty(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageEmpty, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Zero(t, msg.compressed.Len())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Zero(t, msg.data.Len())
	}
	// Should not be possible to advance from empty.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageSend))
}

func checkStageRead(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageRead, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageDecoded(t *testing.T, msg *message) {
	t.Helper()
	require.Equal(t, stageDecoded, msg.stage)
	require.Equal(t, testDataString, msg.msg.(*wrapperspb.StringValue).Value) //nolint:forcetypeassert
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageSend(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		// can't assert anything about m.data: if we didn't have to do
		// anything to get to send (same codec, same compression), we
		// won't have done anything to it; but if we had to re-encode
		// and re-compress, it would get released and set to nil
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	require.Equal(t, stageSend, msg.stage)
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

type fakeCodec struct {
	name                         string
	marshalCalls, unmarshalCalls int
}

func (f *fakeCodec) Name() string {
	return f.name
}

func (f *fakeCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	f.marshalCalls++
	val := msg.(*wrapperspb.StringValue).Value //nolint:forcetypeassert
	return append(b, ([]byte)(val)...), nil
}

func (f *fakeCodec) Unmarshal(b []byte, msg proto.Message) error {
	f.unmarshalCalls++
	msg.(*wrapperspb.StringValue).Value = string(b) //nolint:forcetypeassert
	return nil
}

type fakeCompression struct {
	name                               string
	compressorCalls, decompressorCalls int
	reader                             io.Reader
	writer                             io.Writer
}

func (f *fakeCompression) newPool() *compressionPool {
	return newCompressionPool(
		f.name,
		func() connect.Compressor {
			return (*fakeCompressor)(f)
		},
		func() connect.Decompressor {
			return (*fakeDecompressor)(f)
		},
	)
}

type fakeCompressor fakeCompression

func (f *fakeCompressor) Write(p []byte) (n int, err error) {
	rot13(p)
	return f.writer.Write(p)
}

func (f *fakeCompressor) Close() error {
	return nil
}

func (f *fakeCompressor) Reset(writer io.Writer) {
	(*fakeCompression)(f).compressorCalls++
	f.writer = writer
}

type fakeDecompressor fakeCompression

func (f *fakeDecompressor) Read(p []byte) (n int, err error) {
	n, err = f.reader.Read(p)
	rot13(p[:n])
	return n, err
}

func (f *fakeDecompressor) Close() error {
	return nil
}

func (f *fakeDecompressor) Reset(reader io.Reader) error {
	(*fakeCompression)(f).decompressorCalls++
	f.reader = reader
	return nil
}

func rot13(data []byte) {
	for index, char := range data {
		if char >= 'A' && char <= 'Z' {
			char += 13
			if char > 'Z' {
				char -= 26
			}
		} else if char >= 'a' && char <= 'z' {
			char += 13
			if char > 'z' {
				char -= 26
			}
		}
		data[index] = char
	}
}

type elizaHandler struct {
	elizav1connect.UnimplementedElizaServiceHandler
}

func (elizaHandler) Say(_ context.Context, req *connect.Request[elizav1.SayRequest]) (*connect.Response[elizav1.SayResponse], error) {
	if err := shouldFail(req.Msg.Sentence); err != nil {
		return nil, err
	}
	return connect.NewResponse(&elizav1.SayResponse{
		Sentence: "Oh really? Are you sure " + req.Msg.Sentence + "?",
	}), nil
}

func (elizaHandler) Converse(_ context.Context, str *connect.BidiStream[elizav1.ConverseRequest, elizav1.ConverseResponse]) error {
	for {
		req, err := str.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if err := shouldFail(req.Sentence); err != nil {
			return err
		}
		err = str.Send(&elizav1.ConverseResponse{
			Sentence: "Oh really? Are you sure " + req.Sentence + "?",
		})
		if err != nil {
			return err
		}
	}
}

func (elizaHandler) Introduce(_ context.Context, req *connect.Request[elizav1.IntroduceRequest], str *connect.ServerStream[elizav1.IntroduceResponse]) error {
	err := str.Send(&elizav1.IntroduceResponse{
		Sentence: "Hi, " + req.Msg.Name + ".",
	})
	if err != nil {
		return err
	}
	err = str.Send(&elizav1.IntroduceResponse{
		Sentence: "I'm Eliza.",
	})
	if err != nil {
		return err
	}
	return str.Send(&elizav1.IntroduceResponse{
		Sentence: "Have a nice day!",
	})
}

func shouldFail(str string) error {
	if !strings.HasPrefix(str, "error:") {
		return nil
	}
	parts := strings.SplitN(str, ":", 3)
	msg := parts[1]
	code := connect.CodeUnknown
	if len(parts) == 3 {
		codeStr := parts[2]
		for c := connect.CodeCanceled; c <= connect.CodeUnauthenticated; c++ {
			if c.String() == codeStr {
				code = c
				break
			}
		}
	}
	return connect.NewError(code, errors.New(msg))
}

type protoConnectCodec struct{}

func (p protoConnectCodec) Name() string {
	return CodecProto
}

func (p protoConnectCodec) Marshal(a any) ([]byte, error) {
	msg, ok := a.(proto.Message)
	if !ok {
		return nil, errors.New("not a message")
	}
	return proto.Marshal(msg)
}

func (p protoConnectCodec) Unmarshal(bytes []byte, a any) error {
	msg, ok := a.(proto.Message)
	if !ok {
		return errors.New("not a message")
	}
	return proto.Unmarshal(bytes, msg)
}

type jsonConnectCodec struct{}

func (j jsonConnectCodec) Name() string {
	return CodecJSON
}

func (j jsonConnectCodec) Marshal(a any) ([]byte, error) {
	msg, ok := a.(proto.Message)
	if !ok {
		return nil, errors.New("not a message")
	}
	return protojson.Marshal(msg)
}

func (j jsonConnectCodec) Unmarshal(bytes []byte, a any) error {
	msg, ok := a.(proto.Message)
	if !ok {
		return errors.New("not a message")
	}
	return protojson.Unmarshal(bytes, msg)
}

type addHeaderClientInterceptor struct {
	name, value string
}

func (i addHeaderClientInterceptor) WrapUnary(f connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set(i.name, i.value)
		return f(ctx, req)
	}
}

func (i addHeaderClientInterceptor) WrapStreamingClient(h connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		str := h(ctx, spec)
		str.RequestHeader().Set(i.name, i.value)
		return str
	}
}

func (i addHeaderClientInterceptor) WrapStreamingHandler(h connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return h
}
