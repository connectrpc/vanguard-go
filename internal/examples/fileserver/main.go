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

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
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
	fs := os.DirFS(*directory)
	// Create Connect handler.
	serviceHandler := &ContentService{
		RWFS: &prefixFS{
			FS:     fs,
			prefix: *directory,
		},
	}
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

type RWFS interface {
	fs.FS
	Create(name string) (RWFile, error)
}

type RWFile interface {
	fs.File
	Write([]byte) (int, error)
}

// PrefixFS is something like os.DirFS()
// but now only wraps Create and Open
type prefixFS struct {
	fs.FS
	prefix string
}

func (pf *prefixFS) Create(name string) (RWFile, error) {
	return os.Create(path.Join(pf.prefix, name))
}

func (pf *prefixFS) Open(name string) (fs.File, error) {
	return os.Open(path.Join(pf.prefix, name))
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
	// Since std io fs.Fs is a read-only fs abstraction
	// For some ops like uploading , we need to modify the user file system
	RWFS
}

func (c *ContentService) Index(_ context.Context, req *connect.Request[testv1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error) {
	name := req.Msg.GetPage()
	log.Printf("Index: %v", name)
	if name == "/" || name == "" {
		name = "."
	}

	file, err := c.Open(name)
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
		data, err = fs.ReadFile(c.RWFS, name)
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
		entries, err := fs.ReadDir(c.RWFS, name)
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

// Upload impls the connect RPC stream upload mechanism
// Common Usage(curl):
//
//	```bash
//		echo "hello from nace" > hello.txt
//		curl -X POST --data-binary "@hello.txt" -H "Content-Type: application/octet-stream" localhost:8100/upload_hello.txt:upload
//	```
func (c *ContentService) Upload(
	ctx context.Context,
	stream *connect.ClientStream[testv1.UploadRequest],
) (*connect.Response[emptypb.Empty], error) {
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, err
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no upload message received"))
	}

	msg := stream.Msg()
	filename := msg.GetFilename()

	// NOTE: we currently will truncate/overwrite the file if it already exists
	file, err := c.RWFS.Create(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// OPEN ME for debugging
	// log.Printf("Upload: filename=%q contentType=%q size=%d",
	// 	msg.GetFilename(),
	// 	msg.GetFile().GetContentType(),
	// 	len(msg.GetFile().GetData()),
	// )

	if _, err := file.Write(msg.GetFile().GetData()); err != nil {
		return nil, err
	}

	for stream.Receive() {
		msg := stream.Msg()
		// NOTE: The demo currently only impl the same filename uploading
		if msg.GetFilename() != filename {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filename changed during upload: %q", msg.GetFilename()))
		}
		if _, err := file.Write(msg.GetFile().GetData()); err != nil {
			return nil, err
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// Download impls the connect RPC stream download stream
// Common Usage(curl):
//
//	```bash
//	curl -X GET -H "Content-Type: application/json" http://localhost:8100/upload_hello.txt:download > download_hello.txt
//	```
func (c *ContentService) Download(
	ctx context.Context,
	req *connect.Request[testv1.DownloadRequest],
	stream *connect.ServerStream[testv1.DownloadResponse],
) error {
	file, err := c.RWFS.Open(req.Msg.Filename)
	if err != nil {
		return err
	}
	defer file.Close()

	const largeEnoughSize = 42 * 1024

	buf := make([]byte, largeEnoughSize)
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
