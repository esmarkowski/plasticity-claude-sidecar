// Package settings edits ~/.claude/settings.json without churning it.
//
// The obvious implementation — unmarshal into map[string]any, marshal back —
// silently reorders every key in a file the user hand-maintains and reads
// often. So this keeps each top-level value as the exact bytes it arrived as
// and re-renders only the objects it actually changes.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Object is a JSON object that remembers key order and keeps values verbatim.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

func NewObject() *Object {
	return &Object{vals: map[string]json.RawMessage{}}
}

// ParseObject reads a JSON object, preserving key order.
func ParseObject(b []byte) (*Object, error) {
	o := NewObject()
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected an object key, got %v", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.Set(key, raw)
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return o, nil
}

func (o *Object) Keys() []string { return o.keys }

func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Set replaces a value in place, or appends the key if it is new.
func (o *Object) Set(key string, raw json.RawMessage) {
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

// Render writes the object indented with the given unit, at the given depth.
// Values that were never touched come back out byte-identical apart from
// re-indentation.
func (o *Object) Render(indent string, depth int) []byte {
	if len(o.keys) == 0 {
		return []byte("{}")
	}
	pad := strings.Repeat(indent, depth+1)
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range o.keys {
		key, _ := json.Marshal(k)
		b.WriteString(pad)
		b.Write(key)
		b.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, o.vals[k], pad, indent); err != nil {
			v.Reset()
			v.Write(o.vals[k])
		}
		b.Write(v.Bytes())
		if i < len(o.keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat(indent, depth))
	b.WriteString("}")
	return b.Bytes()
}
