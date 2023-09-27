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

package main

import (
	"bytes"
	"context"
	"flag"
	"html/template"
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
)

func main() {
	flagset := flag.NewFlagSet("fileserver", flag.ExitOnError)
	port := flagset.String("p", "8100", "port to serve on")
	directory := flagset.String("d", ".", "the directory of static file to host")
	if err := flagset.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	contentService := &ContentService{
		FS: os.DirFS(*directory),
	}
	_, contentHandler := testv1connect.NewContentServiceHandler(contentService)

	mux := &vanguard.Mux{}
	if err := mux.RegisterServiceByName(
		contentHandler,
		testv1connect.ContentServiceName,
	); err != nil {
		log.Fatal(err)
	}
	log.Printf("Serving %s on HTTP port: %s\n", *directory, *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux.AsHandler()))
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
	fs.FS
}

func (c *ContentService) Index(_ context.Context, req *connect.Request[testv1.IndexRequest]) (*connect.Response[httpbody.HttpBody], error) {
	name := req.Msg.Page
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
		data, err = fs.ReadFile(c.FS, name)
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
		entries, err := fs.ReadDir(c.FS, name)
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
