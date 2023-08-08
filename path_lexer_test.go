// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLexer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tmpl string
		want tokens
	}{{
		name: "one",
		tmpl: "/v1/messages/{name=name/*}",
		want: tokens{
			{typ: tokenSlash, val: "/", pos: 0},
			{typ: tokenLiteral, val: "v1", pos: 1},
			{typ: tokenSlash, val: "/", pos: 3},
			{typ: tokenLiteral, val: "messages", pos: 4},
			{typ: tokenSlash, val: "/", pos: 12},
			{typ: tokenVariableStart, val: "{", pos: 13},
			{typ: tokenFieldPath, val: "name", pos: 14},
			{typ: tokenEqual, val: "=", pos: 18},
			{typ: tokenLiteral, val: "name", pos: 19},
			{typ: tokenSlash, val: "/", pos: 23},
			{typ: tokenStar, val: "*", pos: 24},
			{typ: tokenVariableEnd, val: "}", pos: 25},
			{typ: tokenEOF, val: "", pos: 26},
		},
	}, {
		name: "two",
		tmpl: "/**/end",
		want: tokens{
			{typ: tokenSlash, val: "/", pos: 0},
			{typ: tokenStarStar, val: "**", pos: 1},
			{typ: tokenSlash, val: "/", pos: 3},
			{typ: tokenLiteral, val: "end", pos: 4},
			{typ: tokenEOF, val: "", pos: 7},
		},
	}, {
		name: "invalid-star-star-star",
		tmpl: "/***",
		want: tokens{
			{typ: tokenSlash, val: "/", pos: 0},
			{typ: tokenStarStar, val: "**", pos: 1},
			{typ: tokenError, val: "unexpected '*'", pos: 3},
		},
	}, {
		name: "invalid-start",
		tmpl: "f/",
		want: tokens{
			{typ: tokenError, val: "expected '/', got 'f'", pos: 0},
		},
	}, {
		name: "hex-value",
		tmpl: "/Hello%2C%20%E4%B8%96%E7%95%8C", // Hello, 世界
		want: tokens{
			{typ: tokenSlash, val: "/", pos: 0},
			{typ: tokenLiteral, val: "Hello%2C%20%E4%B8%96%E7%95%8C", pos: 1},
			{typ: tokenEOF, val: "", pos: 30},
		},
	}}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			toks := lex(testCase.tmpl)
			assert.ElementsMatch(t, testCase.want, toks)
		})
	}
}

func BenchmarkLexer(b *testing.B) {
	var toks tokens
	input := "/v1/books/1/shelves/1:read"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toks = lex(input)
	}
	b.StopTimer()
	if n := len(toks); n != 13 {
		b.Errorf("expected %d tokens: %d", 7, n)
	}
}
