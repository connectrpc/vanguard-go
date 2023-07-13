// Copyright 2023 Buf Technologies, Inc.

package testlibrary

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	connect "github.com/bufbuild/connect-go"
	v1 "github.com/bufbuild/vanguard/internal/gen/library/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch {
	case strings.HasPrefix(r.URL.Path, "/books"):
		switch r.Method {
		case http.MethodGet:
			req := connect.NewRequest(&v1.GetBookRequest{
				Name: r.URL.Path[1:],
			})
			rsp, err := s.GetBook(ctx, req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, rsp.Msg)

		case http.MethodPost:

		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, msg proto.Message) {
	b, err := protojson.MarshalOptions{}.Marshal(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(w, bytes.NewReader(b))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
