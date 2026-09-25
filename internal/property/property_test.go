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

package property_test

import (
	"reflect"
	"testing"

	"github.com/sergelogvinov/go-proxmox-local/internal/property"
)

type testProperties struct {
	Enabled  *bool    `cfg:"enabled,omitempty"`
	Name     string   `cfg:"name,omitempty"`
	Count    *int     `cfg:"count,omitempty"`
	Features []string `cfg:"features,omitempty"`
	Ignored  string   `cfg:"-"`
}

type defaultProperty struct {
	Order *int `cfg:"order,omitempty,default"`
	Up    *int `cfg:"up,omitempty"`
}

func TestMarshal(t *testing.T) {
	enabled := true
	count := 42
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "empty",
			value: testProperties{},
			want:  "",
		},
		{
			name: "all supported fields",
			value: testProperties{
				Enabled:  &enabled,
				Name:     "example",
				Count:    &count,
				Features: []string{"one", "two"},
				Ignored:  "ignored",
			},
			want: "enabled=1,name=example,count=42,features=one;two",
		},
		{
			name: "pointer",
			value: &testProperties{
				Name: "pointer-value",
			},
			want: "name=pointer-value",
		},
		{
			name:  "default property",
			value: defaultProperty{Order: new(2), Up: new(30)},
			want:  "2,up=30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := property.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Marshal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnmarshal(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		target func() any
		want   any
	}{
		{
			name:   "empty",
			input:  "",
			target: func() any { return &testProperties{} },
			want:   testProperties{},
		},
		{
			name:  "all supported fields",
			input: "enabled=1,name=example,count=42,features=one;two,unknown=value",
			target: func() any {
				return &testProperties{}
			},
			want: testProperties{
				Enabled:  new(true),
				Name:     "example",
				Count:    new(42),
				Features: []string{"one", "two"},
			},
		},
		{
			name:   "boolean false",
			input:  "enabled=0",
			target: func() any { return &testProperties{} },
			want:   testProperties{Enabled: new(false)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target()
			if err := property.Unmarshal(tt.input, target); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			got := reflect.ValueOf(target).Elem().Interface()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Unmarshal() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value testProperties
	}{
		{name: "empty", value: testProperties{}},
		{
			name: "populated",
			value: testProperties{
				Enabled:  new(false),
				Name:     "round-trip",
				Count:    new(7),
				Features: []string{"a", "b"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := property.Marshal(&tt.value)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var decoded testProperties
			if err := property.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if !reflect.DeepEqual(decoded, tt.value) {
				t.Fatalf("round trip = %#v, want %#v", decoded, tt.value)
			}
		})
	}
}

func TestMarshalErrorsForUnsupportedType(t *testing.T) {
	_, err := property.Marshal(struct {
		Value float64 `cfg:"value"`
	}{Value: 1.5})
	if err == nil {
		t.Fatal("Marshal() error = nil, want unsupported type error")
	}
}

func TestUnmarshalErrorsForInvalidValue(t *testing.T) {
	var value struct {
		Count int `cfg:"count"`
	}

	if err := property.Unmarshal("count=not-a-number", &value); err == nil {
		t.Fatal("Unmarshal() error = nil, want invalid integer error")
	}
}
