// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"unicode"
	"unicode/utf8"
)

// lexer holds the state of the scanner.
type lexer struct {
	input string // the string being scanned.
	start int    // start position of this token.
	pos   int    // current position in the input.
	width int    // width of last rune read from input.
}

const eof = -1

func (l *lexer) next() rune {
	var char rune
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	if c := l.input[l.pos]; c < utf8.RuneSelf {
		l.width = 1
		char = rune(c)
	} else {
		char, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	}
	l.pos += l.width
	return char
}
func (l *lexer) current() rune {
	var char rune
	switch {
	case l.width == 0:
		return 0
	case l.pos > l.width:
		char, _ = utf8.DecodeRuneInString(l.input[l.pos-l.width:])
	default:
		char, _ = utf8.DecodeRuneInString(l.input)
	}
	return char
}
func (l *lexer) backup() {
	l.pos -= l.width
	l.width = 0
}
func (l *lexer) acceptRun(isValid func(r rune) bool) int {
	count := 0
	for isValid(l.next()) {
		count++
	}
	l.backup()
	return count
}
func (l *lexer) consume(expected rune) bool {
	if l.next() == expected {
		l.discard()
		return true
	}
	return false
}
func (l *lexer) discard() {
	l.start = l.pos
}
func (l *lexer) capture() string {
	value := l.input[l.start:l.pos]
	l.discard()
	return value
}

func isIdent(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsNumber(char) || char == '_' || char == '-'
}
func isFieldPath(char rune) bool {
	return isIdent(char) || char == '.'
}
func isLiteral(char rune) bool {
	if isFieldPath(char) {
		return true
	}
	// Allow all characters that are allowed in a URL path segment, except for
	// the reserved characters that have special meaning in a path segment.
	// https://www.rfc-editor.org/rfc/rfc3986#section-3.3
	switch char {
	case '~', '!', '$', '&', '\'', '(', ')', '+', ',', ';', '=', '@', '%':
		return true
	}
	return false
}
