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
	"testing"
)

func TestDecode(t *testing.T) {
	type member struct {
		Name string   `url:"name"`
		Tags []string `url:"tags"`
	}

	type storage struct {
		ID      string   `url:"storage"`
		Content []string `url:"content"`
		Shared  int      `url:"shared"`
		Ignored string   `url:"-"`
	}

	t.Run("comma-joined string decodes to []string", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{"storage":"local","content":"images,iso,vztmpl","shared":1}`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		want := storage{ID: "local", Content: []string{"images", "iso", "vztmpl"}, Shared: 1, Ignored: ""}
		if !reflect.DeepEqual(s, want) {
			t.Errorf("Decode() = %+v, want %+v", s, want)
		}
	})

	t.Run("empty comma string yields empty slice", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{"storage":"local","content":""}`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if s.Content != nil {
			t.Errorf("Content = %v, want nil", s.Content)
		}
	})

	t.Run("genuine JSON array still decodes", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{"storage":"local","content":["images","iso"]}`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		want := []string{"images", "iso"}
		if !reflect.DeepEqual(s.Content, want) {
			t.Errorf("Content = %v, want %v", s.Content, want)
		}
	})

	t.Run("unknown fields are ignored", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{"storage":"local","future_field":"x"}`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if s.ID != "local" {
			t.Errorf("ID = %q, want %q", s.ID, "local")
		}
	})

	t.Run("url:- field is skipped", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{"Ignored":"x"}`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if s.Ignored != "" {
			t.Errorf("Ignored = %q, want empty", s.Ignored)
		}
	})

	t.Run("writeonly field is skipped even if the key is present", func(t *testing.T) {
		type group struct {
			Name   string `url:"group"`
			Rename string `url:"rename,writeonly"`
		}

		var g group
		if err := Decode([]byte(`{"group":"g1","rename":"old-name"}`), &g); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if g.Name != "g1" {
			t.Errorf("Name = %q, want %q", g.Name, "g1")
		}
		if g.Rename != "" {
			t.Errorf("Rename = %q, want empty (writeonly field must not decode)", g.Rename)
		}
	})

	t.Run("readonly modifier has no effect on Decode", func(t *testing.T) {
		type alias struct {
			Name      string `url:"name"`
			IPVersion int    `url:"ipversion,readonly"`
		}

		var a alias
		if err := Decode([]byte(`{"name":"a1","ipversion":4}`), &a); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if a.IPVersion != 4 {
			t.Errorf("IPVersion = %d, want 4 (readonly only affects Encode)", a.IPVersion)
		}
	})

	t.Run("null data leaves zero value", func(t *testing.T) {
		s := storage{ID: "keep", Content: nil, Shared: 0, Ignored: ""}
		if err := Decode([]byte(`null`), &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if s.ID != "keep" {
			t.Errorf("ID = %q, want %q (unchanged)", s.ID, "keep")
		}
	})

	t.Run("nested struct slice with comma field", func(t *testing.T) {
		type pool struct {
			Members []member `url:"members"`
		}

		var p pool
		data := []byte(`{"members":[{"name":"vm/100","tags":"a,b"},{"name":"vm/101","tags":""}]}`)
		if err := Decode(data, &p); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		want := pool{Members: []member{
			{Name: "vm/100", Tags: []string{"a", "b"}},
			{Name: "vm/101", Tags: nil},
		}}
		if !reflect.DeepEqual(p, want) {
			t.Errorf("Decode() = %+v, want %+v", p, want)
		}
	})

	t.Run("top-level slice of structs", func(t *testing.T) {
		var storages []storage
		data := []byte(`[{"storage":"a","content":"iso"},{"storage":"b","content":"images,iso"}]`)
		if err := Decode(data, &storages); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		want := []storage{
			{ID: "a", Content: []string{"iso"}, Shared: 0, Ignored: ""},
			{ID: "b", Content: []string{"images", "iso"}, Shared: 0, Ignored: ""},
		}
		if !reflect.DeepEqual(storages, want) {
			t.Errorf("Decode() = %+v, want %+v", storages, want)
		}
	})

	t.Run("non-pointer target errors", func(t *testing.T) {
		var s storage
		if err := Decode([]byte(`{}`), s); err == nil {
			t.Fatal("Decode() error = nil, want error")
		}
	})

	t.Run("empty data is a no-op", func(t *testing.T) {
		var s storage
		if err := Decode(nil, &s); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
	})
}

func TestDecodeBool(t *testing.T) {
	type flags struct {
		Enabled  bool  `url:"enabled"`
		Disabled *bool `url:"disabled"`
	}

	wantTrueFalse := func(t *testing.T, f flags) {
		t.Helper()
		if !f.Enabled || f.Disabled == nil || *f.Disabled {
			t.Errorf("f = %+v, want Enabled=true Disabled=false", f)
		}
	}

	t.Run("decodes from genuine JSON bool", func(t *testing.T) {
		var f flags
		if err := Decode([]byte(`{"enabled":true,"disabled":false}`), &f); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		wantTrueFalse(t, f)
	})

	t.Run("decodes from JSON number 1/0", func(t *testing.T) {
		var f flags
		if err := Decode([]byte(`{"enabled":1,"disabled":0}`), &f); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		wantTrueFalse(t, f)
	})

	t.Run("decodes from JSON string 1/0", func(t *testing.T) {
		var f flags
		if err := Decode([]byte(`{"enabled":"1","disabled":"0"}`), &f); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		wantTrueFalse(t, f)
	})

	t.Run("decodes from JSON string true/false", func(t *testing.T) {
		var f flags
		if err := Decode([]byte(`{"enabled":"true","disabled":"false"}`), &f); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		wantTrueFalse(t, f)
	})

	t.Run("errors on unrecognized string", func(t *testing.T) {
		var f flags
		if err := Decode([]byte(`{"enabled":"yes"}`), &f); err == nil {
			t.Fatal("Decode() error = nil, want error")
		}
	})
}

func TestDecodeNumber(t *testing.T) {
	type nums struct {
		Count  int     `url:"count"`
		Total  *int    `url:"total"`
		Shared uint    `url:"shared"`
		Ratio  float64 `url:"ratio"`
	}

	t.Run("decodes from genuine JSON number", func(t *testing.T) {
		var n nums
		if err := Decode([]byte(`{"count":5,"total":10,"shared":2,"ratio":1.5}`), &n); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if n.Count != 5 || n.Total == nil || *n.Total != 10 || n.Shared != 2 || n.Ratio != 1.5 {
			t.Errorf("n = %+v, want Count=5 Total=10 Shared=2 Ratio=1.5", n)
		}
	})

	t.Run("decodes from JSON string", func(t *testing.T) {
		var n nums
		if err := Decode([]byte(`{"count":"5","total":"10","shared":"2","ratio":"1.5"}`), &n); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if n.Count != 5 || n.Total == nil || *n.Total != 10 || n.Shared != 2 || n.Ratio != 1.5 {
			t.Errorf("n = %+v, want Count=5 Total=10 Shared=2 Ratio=1.5", n)
		}
	})

	t.Run("errors on unrecognized string", func(t *testing.T) {
		var n nums
		if err := Decode([]byte(`{"count":"not-a-number"}`), &n); err == nil {
			t.Fatal("Decode() error = nil, want error")
		}
	})
}
