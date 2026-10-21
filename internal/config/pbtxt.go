package config

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const maxPBTXTDepth = 32

var errBadText = errors.New("invalid config text")

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokColon
	tokLBrace
	tokRBrace
	tokLAngle
	tokRAngle
	tokLBracket
	tokRBracket
	tokComma
	tokSemi
)

type token struct {
	kind tokenKind
	text string
}

type valueKind int

const (
	kindString valueKind = iota
	kindNumber
	kindIdent
	kindMessage
	kindList
)

type value struct {
	kind valueKind
	text string
	msg  []field
	list []value
}

type field struct {
	name   string
	values []value
}

type parser struct {
	data  []byte
	i     int
	tok   token
	err   error
	depth int
}

func parsePBTXT(data []byte) ([]field, error) {
	p := &parser{data: data}
	p.bump()
	if p.err != nil {
		return nil, p.err
	}
	if p.tok.kind == tokLBrace || p.tok.kind == tokLAngle {
		val, err := p.parseMessage()
		if err != nil {
			return nil, err
		}
		p.skipSep()
		if p.err != nil {
			return nil, p.err
		}
		if p.tok.kind != tokEOF {
			return nil, errBadText
		}
		return val.msg, nil
	}
	return p.parseFields(tokEOF)
}

func (p *parser) parseFields(end tokenKind) ([]field, error) {
	var fields []field
	for {
		p.skipSep()
		if p.err != nil {
			return nil, p.err
		}
		if p.tok.kind == end {
			if end != tokEOF {
				p.bump()
			}
			if p.err != nil {
				return nil, p.err
			}
			return fields, nil
		}
		if p.tok.kind == tokEOF || p.tok.kind == tokRBrace || p.tok.kind == tokRAngle || p.tok.kind == tokRBracket {
			return nil, errBadText
		}
		name, val, err := p.parseField()
		if err != nil {
			return nil, err
		}
		fields = addField(fields, name, val)
	}
}

func (p *parser) parseField() (string, value, error) {
	if p.tok.kind != tokIdent {
		return "", value{}, errBadText
	}
	name := p.tok.text
	p.bump()
	if p.err != nil {
		return "", value{}, p.err
	}
	switch p.tok.kind {
	case tokColon:
		p.bump()
		if p.err != nil {
			return "", value{}, p.err
		}
		val, err := p.parseValue()
		if err != nil {
			return "", value{}, err
		}
		return name, val, nil
	case tokLBrace, tokLAngle:
		val, err := p.parseMessage()
		if err != nil {
			return "", value{}, err
		}
		return name, val, nil
	case tokLBracket:
		val, err := p.parseList()
		if err != nil {
			return "", value{}, err
		}
		return name, val, nil
	default:
		return "", value{}, errBadText
	}
}

func (p *parser) parseValue() (value, error) {
	switch p.tok.kind {
	case tokLBrace, tokLAngle:
		return p.parseMessage()
	case tokLBracket:
		return p.parseList()
	case tokString:
		val := value{kind: kindString, text: p.tok.text}
		p.bump()
		if p.err != nil {
			return value{}, p.err
		}
		return val, nil
	case tokNumber:
		val := value{kind: kindNumber, text: p.tok.text}
		p.bump()
		if p.err != nil {
			return value{}, p.err
		}
		return val, nil
	case tokIdent:
		val := value{kind: kindIdent, text: p.tok.text}
		p.bump()
		if p.err != nil {
			return value{}, p.err
		}
		return val, nil
	default:
		return value{}, errBadText
	}
}

func (p *parser) parseMessage() (value, error) {
	var end tokenKind
	switch p.tok.kind {
	case tokLBrace:
		end = tokRBrace
	case tokLAngle:
		end = tokRAngle
	default:
		return value{}, errBadText
	}
	if err := p.enter(); err != nil {
		return value{}, err
	}
	defer p.leave()
	p.bump()
	if p.err != nil {
		return value{}, p.err
	}
	fields, err := p.parseFields(end)
	if err != nil {
		return value{}, err
	}
	return value{kind: kindMessage, msg: fields}, nil
}

func (p *parser) parseList() (value, error) {
	if p.tok.kind != tokLBracket {
		return value{}, errBadText
	}
	if err := p.enter(); err != nil {
		return value{}, err
	}
	defer p.leave()
	p.bump()
	if p.err != nil {
		return value{}, p.err
	}
	var items []value
	for {
		p.skipSep()
		if p.err != nil {
			return value{}, p.err
		}
		if p.tok.kind == tokRBracket {
			p.bump()
			if p.err != nil {
				return value{}, p.err
			}
			return value{kind: kindList, list: items}, nil
		}
		if p.tok.kind == tokEOF {
			return value{}, errBadText
		}
		item, err := p.parseValue()
		if err != nil {
			return value{}, err
		}
		items = append(items, item)
	}
}

func (p *parser) enter() error {
	if p.depth == maxPBTXTDepth {
		return errBadText
	}
	p.depth++
	return nil
}

func (p *parser) leave() {
	p.depth--
}

func (p *parser) skipSep() {
	for p.err == nil && (p.tok.kind == tokComma || p.tok.kind == tokSemi) {
		p.bump()
	}
}

func (p *parser) bump() {
	if p.err != nil {
		return
	}
	p.tok, p.err = p.lex()
}

func (p *parser) lex() (token, error) {
	p.skipSpace()
	if p.i >= len(p.data) {
		return token{kind: tokEOF}, nil
	}
	switch p.data[p.i] {
	case ':':
		p.i++
		return token{kind: tokColon}, nil
	case '{':
		p.i++
		return token{kind: tokLBrace}, nil
	case '}':
		p.i++
		return token{kind: tokRBrace}, nil
	case '<':
		p.i++
		return token{kind: tokLAngle}, nil
	case '>':
		p.i++
		return token{kind: tokRAngle}, nil
	case '[':
		p.i++
		return token{kind: tokLBracket}, nil
	case ']':
		p.i++
		return token{kind: tokRBracket}, nil
	case ',':
		p.i++
		return token{kind: tokComma}, nil
	case ';':
		p.i++
		return token{kind: tokSemi}, nil
	case '"', '\'':
		return p.lexString(p.data[p.i])
	default:
		if p.data[p.i] == '-' || isDigit(p.data[p.i]) {
			return p.lexNumber()
		}
		if isIdentStart(p.data[p.i]) {
			return p.lexIdent()
		}
		return token{}, errBadText
	}
}

func (p *parser) skipSpace() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			p.i++
		case '#':
			for p.i < len(p.data) && p.data[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *parser) lexIdent() (token, error) {
	start := p.i
	p.i++
	for p.i < len(p.data) && isIdentCont(p.data[p.i]) {
		p.i++
	}
	return token{kind: tokIdent, text: string(p.data[start:p.i])}, nil
}

func (p *parser) lexNumber() (token, error) {
	start := p.i
	if p.data[p.i] == '-' {
		p.i++
		if p.i >= len(p.data) || !isDigit(p.data[p.i]) {
			return token{}, errBadText
		}
	}
	if p.i+1 < len(p.data) && p.data[p.i] == '0' && (p.data[p.i+1] == 'x' || p.data[p.i+1] == 'X') {
		p.i += 2
		hexStart := p.i
		for p.i < len(p.data) && isHex(p.data[p.i]) {
			p.i++
		}
		if p.i == hexStart {
			return token{}, errBadText
		}
		return token{kind: tokNumber, text: string(p.data[start:p.i])}, nil
	}
	digStart := p.i
	for p.i < len(p.data) && isDigit(p.data[p.i]) {
		p.i++
	}
	if p.i == digStart {
		return token{}, errBadText
	}
	if p.i+1 < len(p.data) && p.data[p.i] == '.' && isDigit(p.data[p.i+1]) {
		p.i++
		for p.i < len(p.data) && isDigit(p.data[p.i]) {
			p.i++
		}
	}
	if p.i < len(p.data) && (p.data[p.i] == 'e' || p.data[p.i] == 'E') {
		p.i++
		if p.i < len(p.data) && (p.data[p.i] == '+' || p.data[p.i] == '-') {
			p.i++
		}
		expStart := p.i
		for p.i < len(p.data) && isDigit(p.data[p.i]) {
			p.i++
		}
		if p.i == expStart {
			return token{}, errBadText
		}
	}
	return token{kind: tokNumber, text: string(p.data[start:p.i])}, nil
}

func (p *parser) lexString(quote byte) (token, error) {
	p.i++
	var b []byte
	for p.i < len(p.data) {
		c := p.data[p.i]
		if c == quote {
			p.i++
			return token{kind: tokString, text: string(b)}, nil
		}
		if c == '\n' {
			return token{}, errBadText
		}
		if c != '\\' {
			b = append(b, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.data) {
			return token{}, errBadText
		}
		decoded, n, err := decodeEscape(p.data[p.i:])
		if err != nil {
			return token{}, err
		}
		b = append(b, decoded...)
		p.i += n
	}
	return token{}, errBadText
}

func decodeEscape(rest []byte) ([]byte, int, error) {
	if len(rest) == 0 {
		return nil, 0, errBadText
	}
	switch rest[0] {
	case 'a':
		return []byte{'\a'}, 1, nil
	case 'b':
		return []byte{'\b'}, 1, nil
	case 'f':
		return []byte{'\f'}, 1, nil
	case 'n':
		return []byte{'\n'}, 1, nil
	case 'r':
		return []byte{'\r'}, 1, nil
	case 't':
		return []byte{'\t'}, 1, nil
	case 'v':
		return []byte{'\v'}, 1, nil
	case '\\', '\'', '"', '?':
		return []byte{rest[0]}, 1, nil
	case 'x':
		if len(rest) < 2 || !isHex(rest[1]) {
			return nil, 0, errBadText
		}
		n := 2
		if len(rest) >= 3 && isHex(rest[2]) {
			n = 3
		}
		v, err := strconv.ParseUint(string(rest[1:n]), 16, 8)
		if err != nil {
			return nil, 0, errBadText
		}
		return []byte{byte(v)}, n, nil
	case 'u', 'U':
		size := 4
		if rest[0] == 'U' {
			size = 8
		}
		if len(rest) < 1+size {
			return nil, 0, errBadText
		}
		for i := 1; i <= size; i++ {
			if !isHex(rest[i]) {
				return nil, 0, errBadText
			}
		}
		v, err := strconv.ParseUint(string(rest[1:1+size]), 16, 32)
		if err != nil || v > unicode.MaxRune || (v >= 0xD800 && v <= 0xDFFF) {
			return nil, 0, errBadText
		}
		return []byte(string(rune(v))), 1 + size, nil
	default:
		if rest[0] < '0' || rest[0] > '7' {
			return nil, 0, errBadText
		}
		n := 1
		for n < 3 && n < len(rest) && rest[n] >= '0' && rest[n] <= '7' {
			n++
		}
		v, err := strconv.ParseUint(string(rest[:n]), 8, 8)
		if err != nil {
			return nil, 0, errBadText
		}
		return []byte{byte(v)}, n, nil
	}
}

func parseIntToken(text string, bitSize int) (int64, error) {
	if floatToken(text) {
		return 0, errBadText
	}
	n, err := strconv.ParseInt(text, numberBase(text), bitSize)
	if err != nil {
		return 0, errBadText
	}
	return n, nil
}

func parseUintToken(text string, bitSize int) (uint64, error) {
	if strings.HasPrefix(text, "-") || floatToken(text) {
		return 0, errBadText
	}
	n, err := strconv.ParseUint(text, numberBase(text), bitSize)
	if err != nil {
		return 0, errBadText
	}
	return n, nil
}

func floatToken(text string) bool {
	if strings.Contains(text, ".") {
		return true
	}
	if strings.Contains(text, "x") || strings.Contains(text, "X") {
		return false
	}
	return strings.ContainsAny(text, "eE")
}

func numberBase(text string) int {
	if strings.Contains(text, "x") || strings.Contains(text, "X") {
		return 0
	}
	return 10
}

func addField(fields []field, name string, val value) []field {
	for i := range fields {
		if fields[i].name == name {
			fields[i].values = append(fields[i].values, val)
			return fields
		}
	}
	return append(fields, field{name: name, values: []value{val}})
}

func findField(fields []field, name string) []value {
	for _, item := range fields {
		if item.name == name {
			return item.values
		}
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}
