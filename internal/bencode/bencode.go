// Package bencode decodes and encodes BitTorrent's bencoding, for reading
// .torrent and libtorrent .fastresume files.
//
// The decoder is written for files Airrbag did not create: it bounds input
// size, nesting depth, element count and string length, and never allocates
// more than the input could justify. Dictionaries keep the raw bytes of every
// value, so the info hash can be computed over the exact original encoding.
package bencode

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 info hashes are SHA-1 by definition
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Limits bound what Decode accepts.
const (
	MaxInput    = 64 << 20 // 64 MiB: far above any real .torrent or .fastresume
	MaxDepth    = 64
	MaxElements = 1 << 20
)

var (
	ErrTooLarge = errors.New("bencode: input too large")
	ErrSyntax   = errors.New("bencode: syntax error")
	ErrDepth    = errors.New("bencode: nesting too deep")
	ErrElements = errors.New("bencode: too many elements")
)

// Value is a decoded element: int64, []byte, []Value or Dict.
type Value any

// Dict is a bencoded dictionary. Raw holds each value's original bytes.
type Dict struct {
	Map map[string]Value
	Raw map[string][]byte
}

// Decode parses one complete bencoded value.
func Decode(b []byte) (Value, error) {
	if len(b) > MaxInput {
		return nil, ErrTooLarge
	}
	d := &decoder{b: b}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.i != len(b) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrSyntax, len(b)-d.i)
	}
	return v, nil
}

type decoder struct {
	b     []byte
	i     int
	count int
}

func (d *decoder) value(depth int) (Value, error) {
	if depth > MaxDepth {
		return nil, ErrDepth
	}
	if d.count++; d.count > MaxElements {
		return nil, ErrElements
	}
	if d.i >= len(d.b) {
		return nil, fmt.Errorf("%w: unexpected end", ErrSyntax)
	}
	switch c := d.b[d.i]; {
	case c == 'i':
		return d.integer()
	case c >= '0' && c <= '9':
		return d.str()
	case c == 'l':
		return d.list(depth)
	case c == 'd':
		return d.dict(depth)
	default:
		return nil, fmt.Errorf("%w: unexpected byte %q", ErrSyntax, c)
	}
}

func (d *decoder) integer() (Value, error) {
	end := bytes.IndexByte(d.b[d.i:], 'e')
	if end < 0 {
		return nil, fmt.Errorf("%w: unterminated integer", ErrSyntax)
	}
	s := string(d.b[d.i+1 : d.i+end])
	if s == "" || s == "-" || s == "-0" || (len(s) > 1 && s[0] == '0') || (len(s) > 2 && s[:2] == "-0") {
		return nil, fmt.Errorf("%w: bad integer %q", ErrSyntax, s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	d.i += end + 1
	return n, nil
}

func (d *decoder) list(depth int) (Value, error) {
	d.i++
	var list []Value
	for {
		if d.i >= len(d.b) {
			return nil, fmt.Errorf("%w: unterminated list", ErrSyntax)
		}
		if d.b[d.i] == 'e' {
			d.i++
			return list, nil
		}
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
}

func (d *decoder) dict(depth int) (Value, error) {
	d.i++
	out := Dict{Map: map[string]Value{}, Raw: map[string][]byte{}}
	for {
		if d.i >= len(d.b) {
			return nil, fmt.Errorf("%w: unterminated dict", ErrSyntax)
		}
		if d.b[d.i] == 'e' {
			d.i++
			return out, nil
		}
		k, err := d.str()
		if err != nil {
			return nil, err
		}
		start := d.i
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		key := string(k)
		out.Map[key] = v
		out.Raw[key] = d.b[start:d.i]
	}
}

func (d *decoder) str() ([]byte, error) {
	colon := bytes.IndexByte(d.b[d.i:], ':')
	if colon < 1 || colon > 10 {
		return nil, fmt.Errorf("%w: bad string length", ErrSyntax)
	}
	n, err := strconv.Atoi(string(d.b[d.i : d.i+colon]))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("%w: bad string length", ErrSyntax)
	}
	start := d.i + colon + 1
	if n > len(d.b)-start {
		return nil, fmt.Errorf("%w: string past end", ErrSyntax)
	}
	d.i = start + n
	return d.b[start:d.i], nil
}

// Int, Str, List and Sub read typed values out of a Dict; ok is false when
// the key is missing or has another type.
func (d Dict) Int(k string) (int64, bool)    { v, ok := d.Map[k].(int64); return v, ok }
func (d Dict) List(k string) ([]Value, bool) { v, ok := d.Map[k].([]Value); return v, ok }
func (d Dict) Str(k string) (string, bool) {
	v, ok := d.Map[k].([]byte)
	return string(v), ok
}
func (d Dict) Sub(k string) (Dict, bool) { v, ok := d.Map[k].(Dict); return v, ok }

// InfoHash is the lower-case hex SHA-1 of the raw "info" value (BitTorrent
// v1), or "" when there is none.
func (d Dict) InfoHash() string {
	raw, ok := d.Raw["info"]
	if !ok {
		return ""
	}
	sum := sha1.Sum(raw) //nolint:gosec // protocol-defined
	return hex.EncodeToString(sum[:])
}

// Encode bencodes int, int64, string, []byte, []any, map[string]any and Dict.
// Dictionary keys are sorted, as the format requires.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := enc(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func enc(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case int:
		fmt.Fprintf(buf, "i%de", x)
	case int64:
		fmt.Fprintf(buf, "i%de", x)
	case string:
		fmt.Fprintf(buf, "%d:%s", len(x), x)
	case []byte:
		fmt.Fprintf(buf, "%d:", len(x))
		buf.Write(x)
	case []any:
		buf.WriteByte('l')
		for _, e := range x {
			if err := enc(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('d')
		for _, k := range keys {
			_ = enc(buf, k)
			if err := enc(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("bencode: cannot encode %T", v)
	}
	return nil
}
