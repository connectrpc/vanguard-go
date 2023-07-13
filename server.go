// Copyright 2023 Buf Technologies, Inc.

package vanguard

import "net/http"

type State sruct {
	// TODO	
}

type Server struct {
	*Config

	state *State
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// map request to service method

	//filter := &filter{}
	//defer filter.OnDestroy()

	//

}
