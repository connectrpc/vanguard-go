// Copyright 2023-2026 Buf Technologies, Inc.
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
	// Allow [-_.~0-9a-zA-Z] and % for escaped characters.
	return isVariable(char) || char == '%'
}

// isVariable is used to determine if a character is allowed in a single variable segment.
//
// See: https://github.com/googleapis/googleapis/blob/master/google/api/http.proto#L251C34-L251C38
func isVariable(char rune) bool {
	// Allow [-_.~0-9a-zA-Z].
	return isFieldPath(char) || char == '-' || char == '~'
}
