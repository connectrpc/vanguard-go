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

// This program serves a directory of files via a Connect-shaped
// ContentService and exposes that service over REST using vanguard.
// Index is unary. Upload and Download are client- and server-streaming
// RPCs, carried over REST because they stream google.api.HttpBody.
//
// Run: go run ./internal/examples/fileserver -d /tmp -p 8100
// Then: curl http://localhost:8100/anyfile.txt
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect/v2"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	flagset := flag.NewFlagSet("fileserver", flag.ExitOnError)
	port := flagset.String("p", "8100", "port to serve on")
	directory := flagset.String("d", ".", "directory of static files to host")
	if err := flagset.Parse(os.Args[1:]); err != nil {
		return err
	}

	root, err := os.OpenRoot(*directory)
	if err != nil {
		return err
	}
	defer root.Close()

	// 1. Build the *connect.Server and register the service impl.
	server := connect.NewServer()
	testv1connect.RegisterContentServiceHandler(server, &contentServer{root: root})

	// 2. Mount the REST router on a mux. connecthttp.Mount would mount
	//    Connect/gRPC routes on the same mux; vanguard handles the REST
	//    half.
	//    Uploads arrive in chunks, so there is no reason to cap them.
	mux := http.NewServeMux()
	if err := vanguard.Mount(mux, server, vanguard.WithMaxReadBytes(0)); err != nil {
		return err
	}

	log.Printf("serving %s on http://localhost:%s\n", *directory, *port)
	return http.ListenAndServe(":"+*port, mux)
}

// contentServer implements ContentServiceHandler over an *os.Root. The
// google.api.HttpBody responses tell vanguard to pass bytes through
// verbatim with the recorded Content-Type.
type contentServer struct {
	testv1connect.UnimplementedContentServiceHandler

	root *os.Root
}

func (c *contentServer) Index(_ context.Context, req *testv1.IndexRequest) (*httpbody.HttpBody, error) {
	name := req.GetPage()
	log.Printf("Index: %q", name)
	if name == "/" || name == "" {
		name = "."
	}
	file, err := c.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		contentType := mime.TypeByExtension(filepath.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		return &httpbody.HttpBody{ContentType: contentType, Data: data}, nil
	}
	// Directory listing.
	entries, err := fs.ReadDir(c.root.FS(), name)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(entries))
	for _, e := range entries {
		files[filepath.Join(name, e.Name())] = e.Name()
	}
	var buf bytes.Buffer
	if err := indexHTMLTemplate.Execute(&buf, struct {
		Title string
		Files map[string]string
	}{Title: name, Files: files}); err != nil {
		return nil, err
	}
	return &httpbody.HttpBody{ContentType: "text/html; charset=utf-8", Data: buf.Bytes()}, nil
}

// Upload receives a client-streaming RPC and writes the file to disk.
// Over REST the whole request body arrives as a single message.
//
// Example with curl:
//
//	curl -X POST --data-binary "@hello.txt" \
//	  -H "Content-Type: application/octet-stream" \
//	  "http://localhost:8100/hello.txt:upload"
func (c *contentServer) Upload(
	_ context.Context,
	stream testv1connect.ContentServiceUploadServerStream,
) (*emptypb.Empty, error) {
	var file *os.File
	for {
		msg, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if file == nil {
			file, err = c.root.Create(msg.GetFilename())
			if err != nil {
				return nil, err
			}
			defer file.Close()
			log.Printf("Upload: %q", msg.GetFilename())
		}
		if _, err := file.Write(msg.GetFile().GetData()); err != nil {
			return nil, err
		}
	}
	if file == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "no upload message received")
	}
	return &emptypb.Empty{}, nil
}

// Download streams a file from disk as a server-streaming RPC. Over REST
// each message is appended to the response body.
//
// Example with curl:
//
//	curl "http://localhost:8100/hello.txt:download" -o hello.txt
func (c *contentServer) Download(
	_ context.Context,
	req *testv1.DownloadRequest,
	stream testv1connect.ContentServiceDownloadServerStream,
) error {
	file, err := c.root.Open(req.GetFilename())
	if err != nil {
		return err
	}
	defer file.Close()
	log.Printf("Download: %q", req.GetFilename())

	buf := make([]byte, 32*1024)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			if err := stream.Send(&testv1.DownloadResponse{
				File: &httpbody.HttpBody{
					ContentType: "application/octet-stream",
					Data:        buf[:n],
				},
			}); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

var indexHTMLTemplate = template.Must(template.New("index").Parse(`
<html>
<head>
  <meta charset="UTF-8">
  <title>{{.Title}}</title>
</head>
<body>
  <pre>
  {{- if ne .Title "."}}
  <a href="/">..</a>
  {{- end}}
  {{- range $path, $name := .Files}}
  <a href="/{{$path}}">{{$name}}</a>
  {{- end}}
  </pre>
</body>
</html>
`))
