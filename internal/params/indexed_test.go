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
	"reflect"
	"strings"
	"testing"
)

// slot is a property-string-like element type: encoded through String,
// decoded through UnmarshalJSON, as the qemu/lxc indexed families are.
type slot struct{ v string }

func (s slot) String() string { return "v=" + s.v }

func (s *slot) UnmarshalJSON(data []byte) error {
	s.v = strings.TrimPrefix(strings.Trim(string(data), `"`), "v=")
	return nil
}

type indexedConfig struct {
	Name     string          `url:"name,omitempty"`
	VirtIO   map[int]slot    `url:"virtio,indexed"`
	VirtioFS map[int]slot    `url:"virtiofs,indexed"`
	Unused   map[int]string  `url:"unused,indexed"`
	RO       map[int]string  `url:"ro,indexed,readonly"`
	EFI      string          `url:"efidisk0,omitempty"`
	Small    map[int8]string `url:"small,indexed"`
}

func TestIndexedRoundTrip(t *testing.T) {
	in := indexedConfig{
		Name:     "vm",
		VirtIO:   map[int]slot{0: {"a"}, 12: {"b"}},
		VirtioFS: map[int]slot{1: {"share"}},
		Unused:   map[int]string{3: ""},
		RO:       map[int]string{0: "never sent"},
		EFI:      "disk",
	}

	p, err := Encode(&in)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	want := map[string]string{
		"name":      "vm",
		"virtio0":   "v=a",
		"virtio12":  "v=b",
		"virtiofs1": "v=share",
		"unused3":   "", // present in the map, so sent even when empty
		"efidisk0":  "disk",
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("Encode() = %v, want %v", p, want)
	}

	data := `{"name":"vm","virtio0":"v=a","virtio12":"v=b","virtiofs1":"v=share",` +
		`"unused3":"","efidisk0":"disk","ro0":"x","small7":"s","virtio":"no index","virtiox1":"not digits"}`

	var out indexedConfig
	if err := Decode([]byte(data), &out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	in.RO = map[int]string{0: "x"}
	in.Small = map[int8]string{7: "s"}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("Decode() = %+v, want %+v", out, in)
	}
}

func TestIndexedAbsentStaysNil(t *testing.T) {
	var out indexedConfig
	if err := Decode([]byte(`{"name":"vm"}`), &out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if out.VirtIO != nil || out.Unused != nil {
		t.Fatalf("absent families allocated: %+v", out)
	}
}

func TestIndexedRequiresIntKeyedMap(t *testing.T) {
	bad := struct {
		Net []string `url:"net,indexed"`
	}{Net: []string{"x"}}

	if _, err := Encode(&bad); err == nil {
		t.Error("Encode() of a non-map indexed field succeeded, want error")
	}

	if err := Decode([]byte(`{"net0":"x"}`), &bad); err == nil {
		t.Error("Decode() into a non-map indexed field succeeded, want error")
	}
}
