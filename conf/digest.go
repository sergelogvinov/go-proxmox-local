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
)

// Digest returns the SHA1 digest of f's raw config text — PVE's own
// optimistic-concurrency token (`qm set --digest <sha1>` rejects a write
// if the file changed since it was read).
//
// For a File obtained from Parse, this hashes the exact bytes Parse
// read, so it always equals the digest pmxcfs/qm computed over the file
// on disk — regardless of any section-ordering choice Format makes when
// re-serializing (see File.Snapshots), which need not match the
// original byte-for-byte for a file with more than one snapshot. For a
// File assembled or modified programmatically (no raw bytes to hash),
// Digest falls back to hashing Format's own output.
func (f *File) Digest() string {
	data := f.raw
	if data == nil {
		var buf bytes.Buffer

		if err := f.Format(&buf); err != nil {
			// bytes.Buffer's Write never errors; Format cannot fail here.
			return ""
		}

		data = buf.Bytes()
	}

	sum := sha1.Sum(data)

	return hex.EncodeToString(sum[:])
}
