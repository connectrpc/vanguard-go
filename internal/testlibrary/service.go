// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package testlibrary

import (
	"context"
	"fmt"

	connect "github.com/bufbuild/connect-go"
	v1 "github.com/bufbuild/vanguard/internal/gen/library/v1"
)

type Service struct {
	Books map[string]*v1.Book
}

func (s *Service) GetBook(ctx context.Context, req *connect.Request[v1.GetBookRequest]) (*connect.Response[v1.Book], error) {
	name := req.Msg.Name
	book, ok := s.Books[name]
	if ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("book %q doesn't exists", name))
	}
	return connect.NewResponse(book), nil

}
func (s *Service) CreateBook(ctx context.Context, req *connect.Request[v1.CreateBookRequest]) (*connect.Response[v1.Book], error) {
	book := req.Msg.Book
	name := book.GetName()
	if _, ok := s.Books[name]; ok {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("book %q already exists", name))
	}
	if s.Books == nil {
		s.Books = make(map[string]*v1.Book)
	}
	s.Books[name] = book
	return connect.NewResponse(book), nil
}
