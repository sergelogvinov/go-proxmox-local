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

// Package property converts Proxmox comma-separated property strings to and
// from tagged Go structs.
package property

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Marshal converts v to a Proxmox property string. Field names and modifiers
// are read from the cfg tag; fields without a tag, or with an empty value, are
// omitted.
func Marshal(v any) (string, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return "", fmt.Errorf("property: expected struct, got %s", rv.Kind())
	}

	var values []string
	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, isDefault := fieldTag(field)
		if name == "" || name == "-" {
			continue
		}

		fv := rv.Field(i)
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}

		value, ok, err := formatValue(fv)
		if err != nil {
			return "", fmt.Errorf("property: field %s: %w", field.Name, err)
		}
		if ok {
			if isDefault {
				values = append(values, value)
			} else {
				values = append(values, name+"="+value)
			}
		}
	}

	return strings.Join(values, ","), nil
}

// Unmarshal parses a Proxmox property string into the struct pointed to by v.
func Unmarshal(s string, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("property: expected a non-nil pointer to struct")
	}

	rv = rv.Elem()
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")

		for i := range rv.NumField() {
			field := rv.Type().Field(i)
			name, isDefault := fieldTag(field)
			if field.PkgPath != "" || (!isDefault && (!ok || name != key)) ||
				(isDefault && ok && name != key) {
				continue
			}
			if err := parseValue(func() string {
				if ok {
					return value
				}
				return key
			}(), rv.Field(i)); err != nil {
				return fmt.Errorf("property: field %s: %w", field.Name, err)
			}
			break
		}
	}

	return nil
}

func cfgName(tag string) string {
	return strings.Split(tag, ",")[0]
}

func fieldTag(field reflect.StructField) (string, bool) {
	name := cfgName(field.Tag.Get("cfg"))
	if slices.Contains(strings.Split(field.Tag.Get("cfg"), ",")[1:], "default") {
		return name, true
	}
	return name, false
}

func formatValue(v reflect.Value) (string, bool, error) {
	switch v.Kind() { //nolint:exhaustive
	case reflect.String:
		return v.String(), v.String() != "", nil
	case reflect.Bool:
		if v.Bool() {
			return "1", true, nil
		}
		return "0", true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true, nil
	case reflect.Slice:
		if v.Len() == 0 {
			return "", false, nil
		}
		parts := make([]string, v.Len())
		for i := range v.Len() {
			parts[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(parts, ";"), true, nil
	default:
		return "", false, fmt.Errorf("unsupported type %s", v.Kind())
	}
}

func parseValue(value string, dst reflect.Value) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return parseValue(value, dst.Elem())
	}

	switch dst.Kind() { //nolint:exhaustive
	case reflect.String:
		dst.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			switch value {
			case "1":
				parsed = true
			case "0":
				parsed = false
			default:
				return err
			}
		}
		dst.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, dst.Type().Bits())
		if err != nil {
			return err
		}
		dst.SetInt(parsed)
	case reflect.Slice:
		parts := strings.Split(value, ";")
		out := reflect.MakeSlice(dst.Type(), len(parts), len(parts))
		for i, part := range parts {
			if err := parseValue(part, out.Index(i)); err != nil {
				return err
			}
		}
		dst.Set(out)
	default:
		return fmt.Errorf("unsupported type %s", dst.Kind())
	}

	return nil
}
