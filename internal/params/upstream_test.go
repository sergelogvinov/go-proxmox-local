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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMatchesUpstream fails when this package drifts from the pinned
// go-proxmox-rest/internal/params. qemu/lxc decode through this copy but
// encode through go-proxmox-rest's EncodeConfig, so Update only sees a
// stable diff while the two copies are identical.
func TestMatchesUpstream(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", "github.com/sergelogvinov/go-proxmox-rest").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}

	upstream := filepath.Join(strings.TrimSpace(string(out)), "internal", "params")

	local, err := goFiles(".")
	if err != nil {
		t.Fatal(err)
	}

	remote, err := goFiles(upstream)
	if err != nil {
		t.Skipf("upstream source not available: %v", err)
	}

	if strings.Join(local, ",") != strings.Join(remote, ",") {
		t.Fatalf("file set differs from %s:\n  local:    %v\n  upstream: %v", upstream, local, remote)
	}

	for _, name := range local {
		a, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(filepath.Join(upstream, name))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from %s; re-sync internal/params with upstream", name, upstream)
		}
	}
}

func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && e.Name() != "upstream_test.go" {
			names = append(names, e.Name())
		}
	}

	sort.Strings(names)

	return names, nil
}
