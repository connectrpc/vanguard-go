// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	_ "google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	_ "google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// getExtensionHTTP
func getExtensionHTTP(m proto.Message) *annotations.HttpRule {
	return proto.GetExtension(m, annotations.E_Http).(*annotations.HttpRule)
}

type variable struct {
	next *path
	name string
	toks tokens
}

func (v *variable) String() string {
	return fmt.Sprintf("%#v", v)
}

type variables []*variable

func (p variables) Len() int           { return len(p) }
func (p variables) Less(i, j int) bool { return p[i].name < p[j].name }
func (p variables) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type path struct {
	segments  map[string]*path   // maps constants to path routes
	methods   map[string]*method // maps http methods to grpc methods
	methodAll *method            // maps kind '*'
	variables variables          // sorted array of variables
}

func (p *path) String() string {
	var s, sp, sv, sm []string
	for k, pp := range p.segments {
		sp = append(sp, "\""+k+"\":"+pp.String())
	}
	if len(sp) > 0 {
		sort.Strings(sp)
		s = append(s, "segments{"+strings.Join(sp, ",")+"}")
	}

	for _, vv := range p.variables {
		sv = append(sv, "\"{"+vv.name+"}\"->"+vv.next.String())
	}
	if len(sv) > 0 {
		sort.Strings(sv)
		s = append(s, "variables["+strings.Join(sv, ",")+"]")
	}

	for k, mm := range p.methods {
		sm = append(sm, "\""+k+"\":"+mm.String())
	}
	if len(sm) > 0 {
		sort.Strings(sm)
		s = append(s, "methods{"+strings.Join(sm, ",")+"}")
	}
	return "path{" + strings.Join(s, ",") + "}"
}

func (p *path) findVariable(name string) (*variable, bool) {
	for _, v := range p.variables {
		if v.name == name {
			return v, true
		}
	}
	return nil, false
}

func (p *path) addVariable(toks tokens) *variable {
	name := toks.String()
	if v, ok := p.findVariable(name); ok {
		return v
	}
	v := &variable{
		name: name,
		toks: toks,
		next: newPath(),
	}
	p.variables = append(p.variables, v)
	sort.Sort(p.variables)
	return v
}

func (p *path) addPath(parent, value token) *path {
	val := parent.val + value.val
	if next, ok := p.segments[val]; ok {
		return next
	}
	next := newPath()
	p.segments[val] = next
	return next
}

func newPath() *path {
	return &path{
		segments: make(map[string]*path),
		methods:  make(map[string]*method),
	}
}

type method struct {
	desc    protoreflect.MethodDescriptor    // protobuf method descriptor
	name    string                           // /{ServiceName}/{MethodName}
	body    []protoreflect.FieldDescriptor   // body
	vars    [][]protoreflect.FieldDescriptor // variables on path
	resp    []protoreflect.FieldDescriptor   // body=[""|"*"]
	hasBody bool                             // body="*" or body="field.name" or body="" for no body
}

func (m *method) String() string {
	return m.name
}

func fieldPath(fieldDescs protoreflect.FieldDescriptors, names ...string) []protoreflect.FieldDescriptor {
	fds := make([]protoreflect.FieldDescriptor, len(names))
	for i, name := range names {
		fd := fieldDescs.ByJSONName(name)
		if fd == nil {
			fd = fieldDescs.ByName(protoreflect.Name(name))
		}
		if fd == nil {
			return nil
		}

		fds[i] = fd

		// advance
		if i != len(fds)-1 {
			msgDesc := fd.Message()
			if msgDesc == nil {
				return nil
			}
			fieldDescs = msgDesc.Fields()
		}
	}
	return fds
}

func (p *path) alive() bool {
	return len(p.methods) != 0 ||
		len(p.variables) != 0 ||
		len(p.segments) != 0
}

// clone deep clones the path tree.
func (p *path) clone() *path {
	pc := newPath()
	if p == nil {
		return pc
	}

	for k, s := range p.segments {
		pc.segments[k] = s.clone()
	}

	pc.variables = make(variables, len(p.variables))
	for i, v := range p.variables {
		pc.variables[i] = &variable{
			name: v.name, // RO
			toks: v.toks, // RO
			next: v.next.clone(),
		}
	}

	for k, m := range p.methods {
		pc.methods[k] = m // RO
	}
	pc.methodAll = p.methodAll

	return pc
}

// delRule deletes the HTTP rule to the path.
func (p *path) delRule(name string) bool {
	for k, s := range p.segments {
		if ok := s.delRule(name); ok {
			if !s.alive() {
				delete(p.segments, k)
			}
			return ok
		}
	}

	for i, v := range p.variables {
		if ok := v.next.delRule(name); ok {
			if !v.next.alive() {
				p.variables = append(
					p.variables[:i], p.variables[i+1:]...,
				)
			}
			return ok
		}
	}

	for k, m := range p.methods {
		if m.name == name {
			delete(p.methods, k)
			return true
		}
	}
	return false
}

// addRule adds the HTTP rule to the path.
func (p *path) addRule(
	rule *annotations.HttpRule,
	desc protoreflect.MethodDescriptor,
	name string,
) error {
	var tmpl, verb string
	switch v := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		verb = http.MethodGet
		tmpl = v.Get
	case *annotations.HttpRule_Put:
		verb = http.MethodPut
		tmpl = v.Put
	case *annotations.HttpRule_Post:
		verb = http.MethodPost
		tmpl = v.Post
	case *annotations.HttpRule_Delete:
		verb = http.MethodDelete
		tmpl = v.Delete
	case *annotations.HttpRule_Patch:
		verb = http.MethodPatch
		tmpl = v.Patch
	case *annotations.HttpRule_Custom:
		verb = strings.ToUpper(v.Custom.Kind)
		tmpl = v.Custom.Path
	default:
		return fmt.Errorf("unsupported pattern %v", v)
	}

	msgDesc := desc.Input()
	fieldDescs := msgDesc.Fields()

	// Hold state for the lexer.
	l := &lexer{input: tmpl}
	if err := lexTemplate(l); err != nil {
		return err
	}

	var (
		i      = 0
		cursor = p
		varfds [][]protoreflect.FieldDescriptor
	)

	next := func() token {
		i++
		return l.toks[i]
	}
	invalid := func(tok token) { panic(fmt.Sprintf("invalid token: %v", tok)) }

	// Segments
	tok := l.toks[i]
	for ; tok.typ == tokenSlash; tok = next() {
		switch val := next(); val.typ {
		// Wildcard
		case tokenStar, tokenStarStar:
			varfds = append(varfds, nil)
			v := cursor.addVariable(l.toks[i : i+1])
			cursor = v.next

		// Literal
		case tokenLiteral:
			cursor = cursor.addPath(tok, val)

		// Variable
		case tokenVariableStart:
			// FieldPath
			tok := next()
			keys := []string{tok.val}

			nxt := next()
			for nxt.typ == tokenDot {
				keys = append(keys, next().val)
				nxt = next()
			}

			var vars tokens
			switch nxt.typ {
			case tokenEqual:
				for nxt := next(); nxt.typ != tokenVariableEnd; nxt = next() {
					vars = append(vars, nxt)
				}

			case tokenVariableEnd:
				// default
				vars = append(vars, token{
					typ: tokenStar,
					val: "*",
				})

			default:
				invalid(nxt)
			}

			fds := fieldPath(fieldDescs, keys...)
			if fds == nil {
				return fmt.Errorf("field not found %v", keys)
			}
			varfds = append(varfds, fds)

			v := cursor.addVariable(vars)
			cursor = v.next

		default:
			invalid(tok)
		}
	}

	switch tok.typ {
	case tokenVerb:
		// Literal
		val := next()
		cursor = cursor.addPath(tok, val)
		// eof

	case tokenEOF:
		// eof

	default:
		invalid(tok)
	}

	if y, ok := cursor.methods[verb]; ok || cursor.methodAll != nil {
		if y.desc.FullName() != desc.FullName() {
			return fmt.Errorf("duplicate rule %v", rule)
		}
		return nil // Method already registered.
	}

	m := &method{
		desc: desc,
		vars: varfds,
		name: name,
	}
	switch rule.Body {
	case "*":
		m.hasBody = true
	case "":
		m.hasBody = false
	default:
		m.body = fieldPath(fieldDescs, strings.Split(rule.Body, ".")...)
		if m.body == nil {
			return fmt.Errorf("body field error %v", rule.Body)
		}
		m.hasBody = true
	}

	switch rule.ResponseBody {
	case "":
	default:
		m.resp = fieldPath(fieldDescs, strings.Split(rule.Body, ".")...)
		if m.resp == nil {
			return fmt.Errorf("response body field error %v", rule.ResponseBody)
		}
	}

	// register method
	if verb == "*" {
		cursor.methodAll = m
	} else {
		cursor.methods[verb] = m
	}

	for _, addRule := range rule.AdditionalBindings {
		if len(addRule.AdditionalBindings) != 0 {
			return fmt.Errorf("nested rules") // TODO: errors...
		}

		if err := p.addRule(addRule, desc, name); err != nil {
			return err
		}
	}
	return nil
}

func quote(raw []byte) []byte {
	if n := len(raw); n > 0 && (raw[0] != '"' || raw[n-1] != '"') {
		raw = strconv.AppendQuote(raw[:0], string(raw))
	}
	return raw
}

type param struct {
	val protoreflect.Value
	fds []protoreflect.FieldDescriptor
}

func parseParam(fds []protoreflect.FieldDescriptor, raw []byte) (param, error) {
	if len(fds) == 0 {
		return param{}, fmt.Errorf("zero field")
	}
	fd := fds[len(fds)-1]

	switch kind := fd.Kind(); kind {
	case protoreflect.BoolKind:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfBool(b)}, nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		var x int32
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfInt32(x)}, nil

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		var x int64
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfInt64(x)}, nil

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		var x uint32
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfUint32(x)}, nil

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		var x uint64
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfUint64(x)}, nil

	case protoreflect.FloatKind:
		var x float32
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfFloat32(x)}, nil

	case protoreflect.DoubleKind:
		var x float64
		if err := json.Unmarshal(raw, &x); err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfFloat64(x)}, nil

	case protoreflect.StringKind:
		return param{fds: fds, val: protoreflect.ValueOfString(string(raw))}, nil

	case protoreflect.BytesKind:
		enc := base64.StdEncoding
		if bytes.ContainsAny(raw, "-_") {
			enc = base64.URLEncoding
		}
		if len(raw)%4 != 0 {
			enc = enc.WithPadding(base64.NoPadding)
		}

		dst := make([]byte, enc.DecodedLen(len(raw)))
		n, err := enc.Decode(dst, raw)
		if err != nil {
			return param{}, err
		}
		return param{fds: fds, val: protoreflect.ValueOfBytes(dst[:n])}, nil

	case protoreflect.EnumKind:
		var x int32
		if err := json.Unmarshal(raw, &x); err == nil {
			return param{fds: fds, val: protoreflect.ValueOfEnum(protoreflect.EnumNumber(x))}, nil
		}

		s := string(raw)
		if isNullValue(fd) && s == "null" {
			return param{fds: fds, val: protoreflect.ValueOfEnum(0)}, nil
		}

		enumVal := fd.Enum().Values().ByName(protoreflect.Name(s))
		if enumVal == nil {
			return param{}, fmt.Errorf("unexpected enum %s", raw)
		}
		return param{fds: fds, val: protoreflect.ValueOfEnum(enumVal.Number())}, nil

	case protoreflect.MessageKind:
		// Well known JSON scalars are decoded to message types.
		md := fd.Message()
		name := string(md.FullName())
		if strings.HasPrefix(name, "google.protobuf.") {
			switch md.FullName()[16:] {
			case "Timestamp":
				var msg timestamppb.Timestamp
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "Duration":
				var msg durationpb.Duration
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "BoolValue":
				var msg wrapperspb.BoolValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "Int32Value":
				var msg wrapperspb.Int32Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "Int64Value":
				var msg wrapperspb.Int64Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "UInt32Value":
				var msg wrapperspb.UInt32Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "UInt64Value":
				var msg wrapperspb.UInt64Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "FloatValue":
				var msg wrapperspb.FloatValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "DoubleValue":
				var msg wrapperspb.DoubleValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "BytesValue":
				var msg wrapperspb.BytesValue
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "StringValue":
				var msg wrapperspb.StringValue
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			case "FieldMask":
				var msg fieldmaskpb.FieldMask
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return param{}, err
				}
				return param{fds: fds, val: protoreflect.ValueOfMessage(msg.ProtoReflect())}, nil
			}
		}
		return param{}, fmt.Errorf("unexpected message type %s", name)

	default:
		return param{}, fmt.Errorf("unknown param type %s", kind)

	}
}

func isNullValue(fd protoreflect.FieldDescriptor) bool {
	ed := fd.Enum()
	return ed != nil && ed.FullName() == "google.protobuf.NullValue"
}

type params []param

func (ps params) set(m proto.Message) error {
	for _, p := range ps {
		cur := m.ProtoReflect()
		for i, fd := range p.fds {
			if len(p.fds)-1 == i {
				switch {
				case fd.IsList():
					l := cur.Mutable(fd).List()
					l.Append(p.val)
				case fd.IsMap():
					return fmt.Errorf("map fields are not supported")
				default:
					cur.Set(fd, p.val)
				}
				break
			}

			cur = cur.Mutable(fd).Message()
		}
	}
	return nil
}

func (m *method) parseQueryParams(values url.Values) (params, error) {
	msgDesc := m.desc.Input()
	fieldDescs := msgDesc.Fields()

	var ps params
	for key, vs := range values {
		fds := fieldPath(fieldDescs, strings.Split(key, ".")...)
		if fds == nil {
			return nil, status.Errorf(codes.InvalidArgument, "unknown query param %q", key)
		}

		for _, v := range vs {
			p, err := parseParam(fds, []byte(v))
			if err != nil {
				return nil, err
			}
			ps = append(ps, p)
		}
	}
	return ps, nil
}

// index returns the capture length.
func (v *variable) index(toks tokens) int {
	n := len(toks)

	var i int
	for _, tok := range v.toks {
		if i == n {
			return -1
		}

		switch tok.typ {
		case tokenSlash:
			if toks[i].typ != tok.typ {
				return -1
			}
			i += 1

		case tokenStar:
			if j := toks.indexAny(tokenSlash | tokenVerb); j != -1 {
				i += j
			} else {
				i = n // EOL
			}

		case tokenStarStar:
			if j := toks.index(tokenVerb); j != -1 {
				i += j
			} else {
				i = n // EOL
			}

		case tokenLiteral:
			// TODO: tokenPath != tokenValue
			if toks[i].typ != tokenPath || tok.val != toks[i].val {
				return -1
			}
			i += 1

		default:
			panic(":(")
		}
	}
	return i
}

var (
	errNotFound = statusErrorf(http.StatusNotFound, 12 /* unimplemented */, "method not allowed")
	errMethod   = statusErrorf(http.StatusMethodNotAllowed, 12 /* unimplemented */, "method not allowed")
)

// Depth first search preferring path segments over variables.
// Variables split the search tree:
//
//	/path/{variable/*}/to/{end/**} ?:VERB
func (p *path) search(toks tokens, verb string) (*method, params, error) {
	if n := len(toks); n <= 1 {
		if m, ok := p.methods[verb]; ok {
			return m, nil, nil
		}
		if m := p.methodAll; m != nil {
			return m, nil, nil
		}
		return nil, nil, errMethod
	}

	// capture path segment
	segment := toks[0].val + toks[1].val
	if next, ok := p.segments[segment]; ok {
		if m, ps, err := next.search(toks[2:], verb); err == nil {
			return m, ps, nil
		} else if err != errNotFound {
			fmt.Println("err", err)
			return nil, nil, err
		}
	}

	for _, v := range p.variables {
		l := v.index(toks[1:]) + 1 // bump off /
		if l == 0 {
			continue
		}

		m, ps, err := v.next.search(toks[l:], verb)
		if err != nil {
			if err != errNotFound {
				return nil, nil, err
			}
			continue
		}

		// fds is nil for non capture variables.
		fds := m.vars[len(m.vars)-len(ps)-1]
		p := param{fds: fds}
		if len(fds) > 0 {
			capture := []byte(toks[1:l].String())

			p, err = parseParam(fds, capture)
			if err != nil {
				return nil, nil, err
			}
		}
		ps = append(ps, p)
		return m, ps, nil
	}
	return nil, nil, errNotFound
}
