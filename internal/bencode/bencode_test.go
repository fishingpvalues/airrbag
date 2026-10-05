package bencode

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := map[string]any{"announce": "https://t.example/a", "info": map[string]any{
		"name": "X", "length": 5, "piece length": 16384, "pieces": "01234567890123456789", "private": 1}}
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	d := v.(Dict)
	info, _ := d.Sub("info")
	if p, _ := info.Int("private"); p != 1 {
		t.Fatalf("private %d", p)
	}
	if n, _ := info.Str("name"); n != "X" {
		t.Fatalf("name %q", n)
	}
	raw, _ := Encode(in["info"])
	if !bytes.Equal(d.Raw["info"], raw) || len(d.InfoHash()) != 40 {
		t.Fatalf("raw info or hash wrong: %q %s", d.Raw["info"], d.InfoHash())
	}
}

func TestRejects(t *testing.T) {
	for in, want := range map[string]error{
		"i01e": ErrSyntax, "i-0e": ErrSyntax, "ie": ErrSyntax, "i1": ErrSyntax,
		"5:abc": ErrSyntax, "l": ErrSyntax, "d1:a": ErrSyntax, "x": ErrSyntax,
		"i1ei2e": ErrSyntax, "99999999999:a": ErrSyntax,
		strings.Repeat("l", MaxDepth+2) + strings.Repeat("e", MaxDepth+2): ErrDepth,
	} {
		if _, err := Decode([]byte(in)); !errors.Is(err, want) {
			t.Errorf("%.20q: got %v, want %v", in, err, want)
		}
	}
	if _, err := Decode(make([]byte, MaxInput+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size limit: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	for _, s := range []string{"d4:infod4:name1:Xee", "li1ei2ee", "3:abc", "i-5e", "d1:al1:bee"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := Decode(b)
		if err != nil {
			return
		}
		// Anything decoded must re-encode to the same bytes (canonical
		// inputs) or at least encode without error.
		if _, err := Encode(toAny(v)); err != nil {
			t.Fatalf("decoded value does not encode: %v", err)
		}
	})
}

func toAny(v Value) any {
	switch x := v.(type) {
	case []Value:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = toAny(e)
		}
		return out
	case Dict:
		out := map[string]any{}
		for k, e := range x.Map {
			out[k] = toAny(e)
		}
		return out
	}
	return v
}
