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
		name      string
		tmpl      string
		want      tokens
		expectErr string
	}{{
		name: "one",
		tmpl: "/v1/messages/{name=name/*}",
		want: tokens{
			{typ: tokenSlash, val: "/"},
			{typ: tokenLiteral, val: "v1"},
			{typ: tokenSlash, val: "/"},
			{typ: tokenLiteral, val: "messages"},
			{typ: tokenSlash, val: "/"},
			{typ: tokenVariableStart, val: "{"},
			{typ: tokenFieldPath, val: "name"},
			{typ: tokenEqual, val: "="},
			{typ: tokenLiteral, val: "name"},
			{typ: tokenSlash, val: "/"},
			{typ: tokenStar, val: "*"},
			{typ: tokenVariableEnd, val: "}"},
			{typ: tokenEOF, val: ""},
		},
	}, {
		name: "two",
		tmpl: "/v1/**/end",
		want: tokens{
			{typ: tokenSlash, val: "/"},
			{typ: tokenLiteral, val: "v1"},
			{typ: tokenSlash, val: "/"},
			{typ: tokenStarStar, val: "**"},
			{typ: tokenSlash, val: "/"},
			{typ: tokenLiteral, val: "end"},
			{typ: tokenEOF, val: ""},
		},
	}, {
		name:      "invalid",
		tmpl:      "/v1/***",
		expectErr: "syntax error at column 7: unexpected '*'",
	}}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			toks, err := lex(testCase.tmpl)
			if testCase.expectErr != "" {
				assert.ErrorContains(t, err, testCase.expectErr)
				return
			}
			assert.NoError(t, err)
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
		var err error
		toks, err = lex(input)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if n := len(toks); n != 13 {
		b.Errorf("expected %d tokens: %d", 7, n)
	}
}
