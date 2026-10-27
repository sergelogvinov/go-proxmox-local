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

// Package params provides a reflection-based encoder/decoder that converts
// between option structs and the flat form parameters / JSON responses used
// by the Proxmox API.
//
// Tag format: `url:"name[,modifier...]"` — see parseTag's doc comment
// (tag.go) for the full grammar, including the "readonly"/"writeonly"
// modifiers Encode and Decode use to let a single struct carry both a
// resource's read and write shape when appropriate. Fields without a tag
// (and unexported fields) are skipped by both Encode and Decode.
//
// Encoding rules:
//   - *T (pointer): encoded iff non-nil; the value is always sent, even
//     when zero — this is how "clear a field" is expressed.
//   - string: skipped when empty.
//   - bool: sent as "1" when true; skipped when false.
//   - int/float: skipped when zero.
//   - []string: comma-joined, skipped when empty.
//   - map[int]T tagged "indexed": one name+index parameter per entry.
//   - a field tagged "readonly" is never sent, regardless of its value.
package params

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Encode converts a struct (or pointer to struct) into Proxmox form
// parameters using the `url` struct tags.
func Encode(v any) (map[string]string, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("params: options must not be nil")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("params: expected struct, got %s", rv.Kind())
	}

	params := map[string]string{}
	rt := rv.Type()

	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}

		tag, ok := parseTag(f.Tag.Get("url"))
		if !ok || tag.readonly {
			continue
		}

		fv := rv.Field(i)

		if tag.indexed {
			if err := encodeIndexed(params, tag.name, fv); err != nil {
				return nil, fmt.Errorf("params: field %s: %w", f.Name, err)
			}

			continue
		}

		// Pointers are encoded iff non-nil; the pointed-to value is
		// always sent so callers can express "set to zero/empty".
		isPtr := fv.Kind() == reflect.Pointer
		if isPtr {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}

		if err := encodeField(params, tag.name, fv, isPtr); err != nil {
			return nil, fmt.Errorf("params: field %s: %w", f.Name, err)
		}
	}

	return params, nil
}

// encodeIndexed encodes an "indexed" map field as one name+index
// parameter per entry. Entries are always sent, like a non-nil pointer:
// putting a key in the map is how a caller asks for that slot to be set.
func encodeIndexed(params map[string]string, name string, fv reflect.Value) error {
	if !isIndexedMap(fv.Type()) {
		return fmt.Errorf("indexed field must be a map with an integer key, got %s", fv.Type())
	}

	iter := fv.MapRange()
	for iter.Next() {
		key := name + strconv.FormatInt(iter.Key().Int(), 10)

		// Copy the value so encodeField can reach a pointer-receiver
		// String method, which map values (not addressable) would hide.
		v := reflect.New(iter.Value().Type()).Elem()
		v.Set(iter.Value())

		if err := encodeField(params, key, v, true); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}

	return nil
}

// isIndexedMap reports whether t can back an "indexed" field: a map
// keyed by a signed integer.
func isIndexedMap(t reflect.Type) bool {
	if t.Kind() != reflect.Map {
		return false
	}

	switch t.Key().Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	default:
		return false
	}
}

// encodeField encodes a single (already dereferenced) field value.
func encodeField(params map[string]string, name string, fv reflect.Value, isPtr bool) error {
	if fv.CanInterface() {
		if stringer, ok := reflect.TypeAssert[fmt.Stringer](fv); ok {
			value := stringer.String()
			if value == "" && !isPtr {
				return nil
			}
			params[name] = value
			return nil
		}
	}
	if fv.CanAddr() && fv.Addr().CanInterface() {
		if stringer, ok := reflect.TypeAssert[fmt.Stringer](fv.Addr()); ok {
			value := stringer.String()
			if value == "" && !isPtr {
				return nil
			}
			params[name] = value
			return nil
		}
	}

	switch fv.Kind() { //nolint:exhaustive
	case reflect.String:
		if !isPtr && fv.String() == "" {
			return nil
		}
		params[name] = fv.String()

	case reflect.Bool:
		if !isPtr && !fv.Bool() {
			return nil
		}

		if fv.Bool() {
			params[name] = "1"
		} else {
			params[name] = "0"
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !isPtr && fv.Int() == 0 {
			return nil
		}
		params[name] = strconv.FormatInt(fv.Int(), 10)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !isPtr && fv.Uint() == 0 {
			return nil
		}
		params[name] = strconv.FormatUint(fv.Uint(), 10)

	case reflect.Float32, reflect.Float64:
		if !isPtr && fv.Float() == 0 {
			return nil
		}
		params[name] = strconv.FormatFloat(fv.Float(), 'f', -1, 64)

	case reflect.Slice, reflect.Array:
		if fv.Len() == 0 {
			return nil
		}
		parts := make([]string, fv.Len())
		for i := range fv.Len() {
			parts[i] = fmt.Sprintf("%v", fv.Index(i).Interface())
		}
		params[name] = strings.Join(parts, ",")

	default:
		return fmt.Errorf("unsupported type %s", fv.Kind())
	}

	return nil
}
