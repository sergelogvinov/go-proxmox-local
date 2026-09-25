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

package conf

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseFormatRoundTrip proves Format(Parse(x)) == x for every real
// config in testdata/ — including a description, a [PENDING] section,
// one or more snapshot sections, and values that would break a YAML
// parser (leading zeros, a bare "on", an internal ':' and '#') but must
// survive conf untouched (docs/design.md §2.3).
func TestParseFormatRoundTrip(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	require.NoError(t, err)

	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
			require.NoError(t, err)

			f, err := Parse(bytes.NewReader(raw))
			require.NoError(t, err)

			var out bytes.Buffer
			require.NoError(t, f.Format(&out))

			assert.Equal(t, string(raw), out.String())
		})
	}
}

func TestParseDescription(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "with_description.conf"))
	require.NoError(t, err)

	f, err := Parse(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "Karpenter discovery service\nnode-capacity VM, do not edit manually", f.Description)
	assert.Equal(t, map[string]string{
		"affinity": "0-7,32-39",
		"cores":    "64",
		"memory":   "current=131072",
		"name":     "node-capacity",
		"numa":     "1",
		"tags":     "karpenter",
	}, f.Current.Map())
}

func TestParsePending(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "with_pending.conf"))
	require.NoError(t, err)

	f, err := Parse(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"cores": "4", "memory": "4096", "name": "test-vm"}, f.Current.Map())
	assert.Equal(t, map[string]string{"cores": "8"}, f.Pending.Map())
}

func TestParseSnapshot(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "with_snapshot.conf"))
	require.NoError(t, err)

	// This is the defect docs/design.md §2.2 describes: a YAML-based
	// parser truncated at "[PENDING]" only, so any other section header
	// (a snapshot's) reached the decoder and failed the whole parse.
	f, err := Parse(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "test-vm", f.Current.Map()["name"])
	require.Contains(t, f.Snapshots, "before-upgrade")
	assert.Equal(t, map[string]string{
		"cores":    "2",
		"memory":   "2048",
		"name":     "test-vm",
		"snaptime": "1710161889",
	}, f.Snapshots["before-upgrade"].Map())
}

func TestParseAwkwardValues(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "awkward_values.conf"))
	require.NoError(t, err)

	f, err := Parse(bytes.NewReader(raw))
	require.NoError(t, err)

	values := f.Current.Map()
	assert.Equal(t, "on", values["onboot"])
	assert.Equal(t, "uuid=3b1e7a0e-6f0a-4a3e-9d3b-2f1a5c6d7e8f,manufacturer=Foo#Bar", values["smbios1"])
	assert.Equal(t, "0000:81:00.0,pcie=1,rombar=0", values["hostpci0"])
}

func TestDigestMatchesRawBytes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "simple.conf"))
	require.NoError(t, err)

	f, err := Parse(bytes.NewReader(raw))
	require.NoError(t, err)

	sum := sha1.Sum(raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), f.Digest())
}

func TestDigestWithoutRawBytesFallsBackToFormat(t *testing.T) {
	f := &File{Current: Section{}}
	f.Current.set("name", "programmatic-vm")

	var out bytes.Buffer
	require.NoError(t, f.Format(&out))

	sum := sha1.Sum(out.Bytes())
	assert.Equal(t, hex.EncodeToString(sum[:]), f.Digest())
}
