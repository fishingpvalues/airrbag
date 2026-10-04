package rtorrent

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// encodeCall builds an XML-RPC methodCall whose params are all strings,
// which is all the read-only rTorrent calls need.
func encodeCall(method string, params ...string) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	_ = xml.EscapeText(&b, []byte(method))
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString(`<param><value><string>`)
		_ = xml.EscapeText(&b, []byte(p))
		b.WriteString(`</string></value></param>`)
	}
	b.WriteString(`</params></methodCall>`)
	return b.Bytes()
}

// xValue is a raw XML-RPC value; decode turns it into: string, int64, float64, bool or []any.
type xValue struct {
	String  *string  `xml:"string"`
	Int     *string  `xml:"int"`
	I4      *string  `xml:"i4"`
	I8      *string  `xml:"i8"`
	Boolean *string  `xml:"boolean"`
	Double  *string  `xml:"double"`
	Array   *xArray  `xml:"array"`
	Struct  *xStruct `xml:"struct"`
	Chars   string   `xml:",chardata"`
}

type xArray struct {
	Data []xValue `xml:"data>value"`
}

type xStruct struct {
	Members []struct {
		Name  string `xml:"name"`
		Value xValue `xml:"value"`
	} `xml:"member"`
}

func (v xValue) decode() (any, error) {
	parseInt := func(s string) (any, error) { return strconv.ParseInt(strings.TrimSpace(s), 10, 64) }
	switch {
	case v.String != nil:
		return *v.String, nil
	case v.I8 != nil:
		return parseInt(*v.I8)
	case v.I4 != nil:
		return parseInt(*v.I4)
	case v.Int != nil:
		return parseInt(*v.Int)
	case v.Boolean != nil:
		return strings.TrimSpace(*v.Boolean) == "1", nil
	case v.Double != nil:
		return strconv.ParseFloat(strings.TrimSpace(*v.Double), 64)
	case v.Array != nil:
		out := make([]any, 0, len(v.Array.Data))
		for _, e := range v.Array.Data {
			d, err := e.decode()
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, nil
	case v.Struct != nil:
		out := map[string]any{}
		for _, m := range v.Struct.Members {
			d, err := m.Value.decode()
			if err != nil {
				return nil, err
			}
			out[m.Name] = d
		}
		return out, nil
	}
	// A bare <value>text</value> is a string by the spec.
	return v.Chars, nil
}

type methodResponse struct {
	Params []struct {
		Value xValue `xml:"value"`
	} `xml:"params>param"`
	Fault *struct {
		Value xValue `xml:"value"`
	} `xml:"fault"`
}

func decodeResponse(raw []byte) (any, error) {
	var r methodResponse
	if err := xml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("rtorrent: decode: %w", err)
	}
	if r.Fault != nil {
		f, _ := r.Fault.Value.decode()
		if m, ok := f.(map[string]any); ok {
			return nil, fmt.Errorf("rtorrent: fault %v: %v", m["faultCode"], m["faultString"])
		}
		return nil, errors.New("rtorrent: fault")
	}
	if len(r.Params) == 0 {
		return nil, errors.New("rtorrent: empty response")
	}
	return r.Params[0].Value.decode()
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return ""
}

func asInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case bool:
		if t {
			return 1
		}
	}
	return 0
}
