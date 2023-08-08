// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenType int

const (
	tokenUnknown = tokenType(iota)
	tokenSlash
	tokenColon
	tokenOpenBrace
	tokenCloseBrace
	tokenDot
	tokenEquals
	tokenWildcard
	tokenIdent
	tokenNonIdent
	tokenSpecialChar
	tokenEOF
)

type lexer struct {
	input string
	// the position of the next token in input; if a token has been
	// peeked, pos points to the location after the peeked token
	pos int

	// the position before the peeked token
	peekedPos int
	// the peeked token's type
	peeked tokenType
	// the peeked token's content
	peekedStr string
}

func (l *lexer) peek() (tokenType, string) {
	if l.peeked == 0 && l.peekedStr == "" {
		l.peekedPos = l.pos
		l.peeked, l.peekedStr = l.next()
	}
	return l.peeked, l.peekedStr
}

func (l *lexer) position() int {
	if l.peeked != 0 || l.peekedStr != "" {
		return l.peekedPos
	}
	return l.pos
}

func (l *lexer) lookingAt(t tokenType) bool {
	peeked, _ := l.peek()
	return t == peeked
}

func (l *lexer) consume(t tokenType) (string, bool) {
	if l.lookingAt(t) {
		_, str := l.next()
		return str, true
	}
	return "", false
}

func (l *lexer) consumeAll(types ...tokenType) (string, int) {
	var count int
	var sb strings.Builder
	for {
		peeked, peekedStr := l.peek()
		var found bool
		for _, t := range types {
			if t == peeked {
				found = true
				break
			}
		}
		if !found {
			return sb.String(), count
		}
		l.next() // consume the peeked token
		count++
		sb.WriteString(peekedStr)
	}
}

func (l *lexer) next() (tokenType, string) {
	if l.peeked != 0 || l.peekedStr != "" {
		t, str := l.peeked, l.peekedStr
		l.peekedPos, l.peeked, l.peekedStr = 0, 0, ""
		return t, str
	}
	decodedRune, runeSz := utf8.DecodeRuneInString(l.input[l.pos:])
	switch decodedRune {
	case utf8.RuneError:
		if runeSz == 0 {
			return tokenEOF, ""
		}
		return tokenUnknown, string(decodedRune)
	case '?':
		l.pos += runeSz
		return tokenUnknown, "?"
	case ' ', '\n', '\r', '\t', '\f', '\v':
		l.pos += runeSz
		return tokenUnknown, string(decodedRune)
	case '%':
		// scan next two hex digits
		pos := l.pos
		count := 0
		str := l.scan(runeSz, func(r rune) bool {
			if !isHexDigit(r) {
				return false
			}
			if count == 2 {
				return false
			}
			count++
			return true
		})
		if count != 2 {
			// invalid special char; just return the percent sign
			l.pos = pos + runeSz
			return tokenUnknown, string(decodedRune)
		}
		return tokenSpecialChar, str
	case '/':
		l.pos += runeSz
		return tokenSlash, "/"
	case ':':
		l.pos += runeSz
		return tokenColon, ":"
	case '{':
		l.pos += runeSz
		return tokenOpenBrace, "{"
	case '}':
		l.pos += runeSz
		return tokenCloseBrace, "}"
	case '.':
		l.pos += runeSz
		return tokenDot, "."
	case '=':
		l.pos += runeSz
		return tokenEquals, "="
	case '*':
		l.pos += runeSz
		return tokenWildcard, "*"
	default:
		if decodedRune == '_' || (decodedRune >= 'a' && decodedRune <= 'z') || (decodedRune >= 'A' && decodedRune <= 'Z') {
			return tokenIdent, l.scan(runeSz, isIdentifier)
		}
		return tokenNonIdent, l.scan(runeSz, isNonIdentifier)
	}
}

func (l *lexer) scan(skip int, keep func(rune) bool) string {
	for {
		decodedRune, runeSz := utf8.DecodeRuneInString(l.input[l.pos+skip:])
		if !keep(decodedRune) {
			ret := l.input[l.pos : l.pos+skip]
			l.pos += skip
			return ret
		}
		skip += runeSz
	}
}

func (l *lexer) syntaxError(errMsg string) error {
	return &syntaxError{
		pos:    l.position(),
		input:  l.input,
		errMsg: errMsg,
	}
}

func isIdentifier(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isNonIdentifier(r rune) bool {
	return isDigit(r) || (!isIdentifier(r) && !strings.ContainsRune("/:{}.=*%? \n\r\t\f\v", r))
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

type syntaxError struct {
	pos           int
	input, errMsg string
}

// Error renders syntax errors that show the location of the problem, like so:
//
//	/foo/{bar/baz}/buzz
//	         ^
//	syntax error at column 10: expecting '}'
func (s *syntaxError) Error() string {
	return fmt.Sprintf("%s\n%s^\nsyntax error at column %d: %s", s.input, strings.Repeat(" ", s.pos), s.pos+1, s.errMsg)
}
