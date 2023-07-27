// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import "testing"

func TestConvert(t *testing.T) {
	upstream := []protocol{
		protocolHTTP,
	}
	downstream := []protocol{
		protocolGRPC,
	}
	m := &Mux{}

	for _, u := range upstream {
		for _, d := range downstream {
			_, err := m.convert(u, d)
			if err != nil {
				t.Errorf("convert(%s, %s) failed: %v", u, d, err)
			}
		}
	}
}
