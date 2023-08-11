// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"unicode/utf8"
)

// pathScanner holds the state of the scanner.
type pathScanner struct {
	input string // the string being scanned.
	start int    // start position of this token.
	pos   int    // current position in the input.
	width int    // width of last rune read from input.
}

const eof = -1

func (s *pathScanner) next() rune {
	var char rune
	if s.pos >= len(s.input) {
		s.width = 0
		return eof
	}
	char, s.width = utf8.DecodeRuneInString(s.input[s.pos:])
	s.pos += s.width
	return char
}
func (s *pathScanner) current() rune {
	var char rune
	if s.width > 0 {
		char, _ = utf8.DecodeRuneInString(s.input[s.pos-s.width:])
	} else if s.pos > len(s.input) {
		char = eof
	}
	return char
}
func (s *pathScanner) backup() {
	s.pos -= s.width
	s.width = 0
}
func (s *pathScanner) captureRun(isValid func(r rune) bool) string {
	for isValid(s.next()) {
		continue
	}
	s.backup()
	return s.capture()
}
func (s *pathScanner) consume(expected rune) bool {
	if s.next() == expected {
		s.discard()
		return true
	}
	return false
}
func (s *pathScanner) discard() {
	s.start = s.pos
}
func (s *pathScanner) capture() string {
	value := s.input[s.start:s.pos]
	s.discard()
	return value
}

func isIdentStart(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_'
}
func isDigit(char rune) bool {
	return char >= '0' && char <= '9'
}
func isIdent(char rune) bool {
	return isIdentStart(char) || isDigit(char)
}
func isFieldPath(char rune) bool {
	return isIdent(char) || char == '.'
}
func isLiteral(char rune) bool {
	// Allow all characters that are allowed in a URL path segment.
	// Follows [url.PathUnescape](https://golang.org/pkg/net/url/#PathUnescape).
	// [https://cs.opensource.google/go/go/+/refs/tags/go1.21.0:src/net/url/url.go;l=138]
	return isFieldPath(char) || char == '%' || char == '~' || char == '-' || char == '$' || char == '&' || char == '+' || char == '=' || char == '@'
}
