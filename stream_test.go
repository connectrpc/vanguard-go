// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import "testing"

func TestConvert(t *testing.T) {
	upstream := []protocol{
		protocolHTTPRule,
	}
	downstream := []protocol{
		protocolGRPC,
	}

	for _, u := range upstream {
		for _, d := range downstream {
			_, err := convert(nil, u, d)
			if err != nil {
				t.Errorf("convert(%s, %s) failed: %v", u, d, err)
			}
		}
	}
}
