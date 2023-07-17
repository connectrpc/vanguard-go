// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ### Path template syntax
//
//     Template = "/" Segments [ Verb ] ;
//     Segments = Segment { "/" Segment } ;
//     Segment  = "*" | "**" | LITERAL | Variable ;
//     Variable = "{" FieldPath [ "=" Segments ] "}" ;
//     FieldPath = IDENT { "." IDENT } ;
//     Verb     = ":" LITERAL ;

type tokenType uint16

const (
	tokenError         tokenType = 0
	tokenSlash         tokenType = 1 << iota // /
	tokenStar                                // *
	tokenStarStar                            // **
	tokenVariableStart                       // {
	tokenVariableEnd                         // }
	tokenEqual                               // =
	tokenIdent                               // a-z A-Z 0-9 - _
	tokenLiteral                             // a-z A-Z 0-9 - _ .
	tokenDot                                 // .
	tokenVerb                                // :
	tokenPath                                // a-z A-Z 0-9 - _ . ~ ! $ & ' ( ) * + , ; = @
	tokenEOF
)

type token struct {
	val string
	typ tokenType
}

type tokens []token

func (toks tokens) String() string {
	var b strings.Builder
	for _, tok := range toks {
		b.WriteString(tok.val)
	}
	return b.String()
}

func (toks tokens) index(typ tokenType) int {
	for i, tok := range toks {
		if tok.typ == typ {
			return i
		}
	}
	return -1
}

func (toks tokens) indexAny(s tokenType) int {
	for i, tok := range toks {
		if s&tok.typ != 0 {
			return i
		}
	}
	return -1
}

type lexer struct {
	input string
	toks  [64]token
	len   int
	start int
	pos   int
	width int
}

func (l *lexer) tokens() tokens { return l.toks[:l.len] }

const eof = -1

func (l *lexer) next() (r rune) {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	if c := l.input[l.pos]; c < utf8.RuneSelf {
		l.width = 1
		r = rune(c)
	} else {
		r, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	}
	l.pos += l.width
	return r
}

func (l *lexer) current() (r rune) {
	if l.width == 0 {
		return 0
	} else if l.pos > l.width {
		r, _ = utf8.DecodeRuneInString(l.input[l.pos-l.width:])
	} else {
		r, _ = utf8.DecodeRuneInString(l.input)
	}
	return r
}

func (l *lexer) backup() {
	l.pos -= l.width
}

func (l *lexer) acceptRun(isValid func(r rune) bool) int {
	var i int
	for isValid(l.next()) {
		i++
	}
	l.backup()
	return i
}

var errTokenLimit = status.Errorf(codes.InvalidArgument, "path: too many tokens")

func (l *lexer) emit(typ tokenType) error {
	if l.len >= len(l.toks) {
		return errTokenLimit
	}
	tok := token{typ: typ, val: l.input[l.start:l.pos]}

	l.toks[l.len] = tok
	l.len++
	l.start = l.pos
	return nil
}

func (l *lexer) errUnexpected() error {
	if err := l.emit(tokenError); err != nil {
		return err
	}
	r := l.current()
	return status.Errorf(codes.InvalidArgument, "path: %v:%v unexpected rune %q", l.pos-l.width, l.pos, r)
}
func (l *lexer) errShort() error {
	if err := l.emit(tokenError); err != nil {
		return err
	}
	r := l.current()
	return status.Errorf(codes.InvalidArgument, "path: %v:%v short read %q", l.pos-l.width, l.pos, r)
}

func isIdent(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}

func isLiteral(r rune) bool {
	return isIdent(r) || r == '.'
}

func isPath(r rune) bool {
	return isLiteral(r) || r == '~' || r == '!' || r == '$' || r == '&' ||
		r == '\'' || r == '(' || r == ')' || r == '*' || r == '+' ||
		r == ',' || r == ';' || r == '=' || r == '@'
}

func lexIdent(l *lexer) error {
	if i := l.acceptRun(isIdent); i == 0 {
		return l.errShort()
	}
	return l.emit(tokenIdent)
}

func lexLiteral(l *lexer) error {
	if i := l.acceptRun(isLiteral); i == 0 {
		return l.errShort()
	}
	return l.emit(tokenLiteral)
}

func lexFieldPath(l *lexer) error {
	if err := lexIdent(l); err != nil {
		return err
	}
	for {
		if r := l.next(); r != '.' {
			l.backup() // unknown
			return nil
		}
		if err := l.emit(tokenDot); err != nil {
			return err
		}
		if err := lexIdent(l); err != nil {
			return err
		}
	}
}

func lexVerb(l *lexer) error {
	if err := lexLiteral(l); err != nil {
		return err
	}
	if r := l.next(); r == eof {
		return l.emit(tokenEOF)
	}
	return l.errUnexpected()
}

func lexVariable(l *lexer) error {
	r := l.next()
	if r != '{' {
		return l.errUnexpected()
	}
	if err := l.emit(tokenVariableStart); err != nil {
		return err
	}
	if err := lexFieldPath(l); err != nil {
		return err
	}

	r = l.next()
	if r == '=' {
		if err := l.emit(tokenEqual); err != nil {
			return err
		}

		if err := lexSegments(l); err != nil {
			return err
		}
		r = l.next()
	}

	if r != '}' {
		return l.errUnexpected()
	}
	return l.emit(tokenVariableEnd)
}

func lexSegment(l *lexer) error {
	r := l.next()
	switch {
	case unicode.IsLetter(r):
		return lexLiteral(l)
	case r == '*':
		rn := l.next()
		if rn == '*' {
			return l.emit(tokenStarStar)
		}
		l.backup()
		return l.emit(tokenStar)
	case r == '{':
		l.backup()
		return lexVariable(l)
	default:
		return l.errUnexpected()
	}
}

func lexSegments(l *lexer) error {
	for {
		if err := lexSegment(l); err != nil {
			return err
		}
		if r := l.next(); r != '/' {
			l.backup() // unknown
			return nil
		}
		if err := l.emit(tokenSlash); err != nil {
			return err
		}
	}
}

func lexTemplate(l *lexer) error {
	if r := l.next(); r != '/' {
		return l.errUnexpected()
	}
	if err := l.emit(tokenSlash); err != nil {
		return err
	}
	if err := lexSegments(l); err != nil {
		return err
	}

	switch r := l.next(); r {
	case ':':
		if err := l.emit(tokenVerb); err != nil {
			return err
		}
		return lexVerb(l)
	case eof:
		if err := l.emit(tokenEOF); err != nil {
			return err
		}
		return nil
	default:
		return l.errUnexpected()
	}
}

func lexPathSegment(l *lexer) error {
	if i := l.acceptRun(isPath); i == 0 {
		return l.errShort()
	}
	return l.emit(tokenPath)
}

// lexPath emits all tokenSlash, tokenVerb and the rest as tokenPath
func lexPath(l *lexer) error {
	for {
		switch r := l.next(); r {
		case '/':
			if err := l.emit(tokenSlash); err != nil {
				return err
			}
			if err := lexPathSegment(l); err != nil {
				return err
			}
		case ':':
			if err := l.emit(tokenVerb); err != nil {
				return err
			}
			if err := lexPathSegment(l); err != nil {
				return err
			}
		case eof:
			return l.emit(tokenEOF)
		default:
			return l.errUnexpected()
		}
	}
}

func lexServiceName(l *lexer) error {
	for {
		switch r := l.next(); r {
		case '/':
			if err := l.emit(tokenSlash); err != nil {
				return err
			}
			if err := lexPathSegment(l); err != nil {
				return err
			}
		case eof:
			return l.emit(tokenEOF)
		default:
			return l.errUnexpected()
		}
	}
}
