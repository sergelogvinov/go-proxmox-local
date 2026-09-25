/*
Copyright 2026 Proxmox Community.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package params

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Decode is the reverse of Encode: it unmarshals a Proxmox API JSON payload
// into out (a non-nil pointer), recursing into nested structs and slices of
// structs using the same `url` struct tags Encode uses (see tag.go's
// parseTag), so a single tag per field names it on the wire for both
// directions. This means every field a caller wants populated from a GET
// response must carry a `url` tag with its wire name — a bare `json` tag
// is not enough, since Decode does not consult it.
//
// A field tagged "writeonly" (e.g. `url:"rename,writeonly"`) is always
// skipped by Decode, even if the response happens to contain a matching
// key — this is for write-only parameters that share a struct with a
// resource's read fields but never appear in a GET response.
//
// It behaves like encoding/json for every field except two:
//
//   - A field of type []string is, like Encode's comma-join, allowed to
//     arrive on the wire as a single JSON string (e.g.
//     "content":"images,iso,vztmpl") rather than a JSON array. Decode splits
//     that string on "," into the slice. A field already sent as a genuine
//     JSON array decodes exactly as encoding/json would.
//   - A field of type bool (or *bool) is allowed to arrive on the wire as a
//     JSON number (1/0) or a JSON string ("1"/"0"/"true"/"false"), in
//     addition to a genuine JSON bool, matching Proxmox's inconsistent
//     encoding of boolean fields across endpoints.
//   - A numeric field (int/uint/float, or a pointer to one) is allowed to
//     arrive on the wire as a JSON string (e.g. "5"), in addition to a
//     genuine JSON number, matching Proxmox's inconsistent encoding of
//     numeric fields across endpoints.
func Decode(data []byte, out any) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("params: decode target must be a non-nil pointer")
	}

	return decodeValue(data, rv.Elem())
}

// decodeValue decodes raw into the addressable value rv, recursing into
// structs and slices of structs so nested DTOs get the same []string
// handling as their parent.
func decodeValue(raw json.RawMessage, rv reflect.Value) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	if rv.Kind() == reflect.Pointer {
		rv.Set(reflect.New(rv.Type().Elem()))
		return decodeValue(raw, rv.Elem())
	}

	if rv.CanAddr() {
		if unmarshaler, ok := reflect.TypeAssert[json.Unmarshaler](rv.Addr()); ok {
			return unmarshaler.UnmarshalJSON(raw)
		}
	}

	switch {
	case rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.String && raw[0] == '"':
		return decodeCommaList(raw, rv)

	case rv.Kind() == reflect.Bool:
		return decodeBool(raw, rv)

	case isNumericKind(rv.Kind()):
		return decodeNumber(raw, rv)

	case rv.Kind() == reflect.Struct:
		return decodeStruct(raw, rv)

	case rv.Kind() == reflect.Slice && elemIsStruct(rv.Type().Elem()):
		return decodeStructSlice(raw, rv)

	default:
		return json.Unmarshal(raw, rv.Addr().Interface())
	}
}

// decodeStruct decodes a JSON object into rv field-by-field, matching each
// field's `url` tag name.
func decodeStruct(raw json.RawMessage, rv reflect.Value) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}

		name, _, writeonly, ok := parseTag(f.Tag.Get("url"))
		if !ok || writeonly {
			continue
		}
		if name == "" {
			if f.Anonymous {
				// An embedded field with no explicit wire name (a bare
				// `,inline` tag, e.g. cluster.statusEntry's NodeStatus) is
				// flattened: decode the same object into it, mirroring
				// encoding/json's own embedding behavior, instead of
				// looking it up under its Go type name as a nested key.
				if err := decodeValue(raw, rv.Field(i)); err != nil {
					return fmt.Errorf("field %s: %w", f.Name, err)
				}
				continue
			}
			name = f.Name
		}

		item, present := m[name]
		if !present {
			continue
		}

		if err := decodeValue(item, rv.Field(i)); err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
	}

	return nil
}

// decodeStructSlice decodes a JSON array of objects into rv, applying
// decodeValue (and therefore struct/[]string handling) to each element.
func decodeStructSlice(raw json.RawMessage, rv reflect.Value) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}

	elemType := rv.Type().Elem()
	out := reflect.MakeSlice(rv.Type(), len(items), len(items))

	for i, item := range items {
		ev := out.Index(i)
		if elemType.Kind() == reflect.Pointer {
			ev.Set(reflect.New(elemType.Elem()))
			ev = ev.Elem()
		}
		if err := decodeValue(item, ev); err != nil {
			return err
		}
	}

	rv.Set(out)

	return nil
}

// decodeCommaList splits a JSON string on "," into rv, mirroring Encode's
// comma-join for []string fields.
func decodeCommaList(raw json.RawMessage, rv reflect.Value) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	out := reflect.MakeSlice(rv.Type(), len(parts), len(parts))
	for i, p := range parts {
		out.Index(i).SetString(p)
	}
	rv.Set(out)

	return nil
}

// decodeBool decodes raw into the bool rv, accepting a genuine JSON bool, a
// JSON number (nonzero is true), or a JSON string ("1"/"0"/"true"/"false"),
// since Proxmox encodes boolean fields inconsistently across endpoints.
func decodeBool(raw json.RawMessage, rv reflect.Value) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}

	switch t := v.(type) {
	case bool:
		rv.SetBool(t)

	case float64:
		rv.SetBool(t != 0)

	case string:
		switch strings.ToLower(t) {
		case "1", "true":
			rv.SetBool(true)
		case "0", "false", "":
			rv.SetBool(false)
		default:
			return fmt.Errorf("params: cannot decode %q as bool", t)
		}

	default:
		return fmt.Errorf("params: cannot decode %T as bool", v)
	}

	return nil
}

// isNumericKind reports whether k is a Go int/uint/float kind, i.e. one
// decodeNumber knows how to populate.
func isNumericKind(k reflect.Kind) bool {
	switch k { //nolint:exhaustive
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// decodeNumber decodes raw into the numeric rv, accepting a genuine JSON
// number or a JSON string holding one (e.g. "5"), since Proxmox encodes
// numeric fields inconsistently across endpoints.
func decodeNumber(raw json.RawMessage, rv reflect.Value) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}

	var f float64

	switch t := v.(type) {
	case float64:
		f = t

	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return fmt.Errorf("params: cannot decode %q as number", t)
		}
		f = parsed

	default:
		return fmt.Errorf("params: cannot decode %T as number", v)
	}

	switch rv.Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		rv.SetInt(int64(f))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		rv.SetUint(uint64(f))

	case reflect.Float32, reflect.Float64:
		rv.SetFloat(f)
	}

	return nil
}

// elemIsStruct reports whether t (or the type it points to) is a struct.
func elemIsStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}
