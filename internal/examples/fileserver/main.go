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

	"connectrpc.com/connect"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	flagset := flag.NewFlagSet("fileserver", flag.ExitOnError)
	port := flagset.String("p", "8100", "port to serve on")
	directory := flagset.String("d", ".", "the directory of static file to host")
	if err := flagset.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	root, err := os.OpenRoot(*directory)
	if err != nil {
		log.Fatal(err)
	}
	defer root.Close()

	// Create Connect handler.
	serviceHandler := &ContentService{root: root}
	// And wrap it with Vanguard.
	service := vanguard.NewService(testv1connect.NewContentServiceHandler(serviceHandler))
	handler, err := vanguard.NewTranscoder([]*vanguard.Service{service})
	if err != nil {
		log.Fatal(err)
	}
	// Now handler also supports REST requests, translated to Connect
	// using the HTTP annotations on the ContentService definition.
	log.Printf("Serving %s on HTTP port: %s\n", *directory, *port)
	log.Fatal(http.ListenAndServe(":"+*port, handler))
}

var indexHTMLTemplate = template.Must(template.New("http").Parse(`
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

type ContentService struct {
	testv1connect.UnimplementedContentServiceHandler
	root *os.Root
}

func (c *ContentService) Index(_ context.Context, req *connect.Request[testv1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error) {
	name := req.Msg.GetPage()
	log.Printf("Index: %v", name)
	if name == "/" || name == "" {
		name = "."
	}

	file, err := c.root.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	contentType := "text/html"
	var data []byte
	if !stat.IsDir() {
		contentType = mime.TypeByExtension(filepath.Ext(name))
		data, err = fs.ReadFile(c.root.FS(), name)
		if err != nil {
			return nil, err
		}
	} else {
		tmplData := struct {
			Title string
			Files map[string]string
		}{
			Title: name,
			Files: make(map[string]string),
		}
		entries, err := fs.ReadDir(c.root.FS(), name)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			tmplData.Files[filepath.Join(name, entry.Name())] = entry.Name()
		}
		var buf bytes.Buffer
		if err := indexHTMLTemplate.Execute(&buf, tmplData); err != nil {
			return nil, err
		}
		data = buf.Bytes()
	}

	return connect.NewResponse(&httpbody.HttpBody{
		ContentType: contentType,
		Data:        data,
	}), nil
}

// Upload receives a client-streaming RPC and writes the file to disk.
//
// Example with curl:
//
//	curl -X POST --data-binary "@hello.txt" \
//	  -H "Content-Type: application/octet-stream" \
//	  "http://localhost:8100/hello.txt:upload"
func (c *ContentService) Upload(
	_ context.Context,
	stream *connect.ClientStream[testv1.UploadRequest],
) (*connect.Response[emptypb.Empty], error) {
	var file *os.File
	for stream.Receive() {
		msg := stream.Msg()
		if file == nil {
			var err error
			file, err = c.root.Create(msg.GetFilename())
			if err != nil {
				return nil, err
			}
			defer file.Close() //nolint:gocritic
			log.Printf("Upload: %q", msg.GetFilename())
		}
		if _, err := file.Write(msg.GetFile().GetData()); err != nil {
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if file == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no upload message received"))
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

// Download streams a file from disk as a server-streaming RPC.
//
// Example with curl:
//
//	curl "http://localhost:8100/hello.txt:download" -o hello.txt
func (c *ContentService) Download(
	_ context.Context,
	req *connect.Request[testv1.DownloadRequest],
	stream *connect.ServerStream[testv1.DownloadResponse],
) error {
	file, err := c.root.Open(req.Msg.GetFilename())
	if err != nil {
		return err
	}
	defer file.Close()
	log.Printf("Download: %q", req.Msg.GetFilename())

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
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
