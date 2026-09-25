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

import "strings"

// parseTag parses a `url:"name[,modifier...]"` struct tag — the format
// shared by Encode and Decode. ok reports whether the field participates in
// wire mapping at all: a missing tag or "-" (matching encoding/json's skip
// convention) returns ok=false and the field is ignored by both functions.
//
// name is the tag's first (comma-separated) segment, exactly as written —
// callers that want Decode's long-standing "empty name falls back to the
// Go field name" behavior apply that themselves; Encode has never done so
// and this preserves that.
//
// Recognized modifiers, found anywhere after the name:
//   - "readonly": Encode never sends this field, even when it holds a
//     non-zero value. For fields that only ever appear in a GET response —
//     a server-computed digest, a rule's position, a resolved IP version —
//     this lets a single struct carry both the read and write shape of a
//     resource without risking a stale/read-only value leaking into a
//     write request when a caller builds Update options by copying a
//     previously-fetched struct.
//   - "writeonly": Decode never populates this field. For parameters that
//     only make sense on a write (a rename target, a delete-list) and
//     never appear in a GET response.
//   - "omitempty": accepted for symmetry with the sibling `json` tag, and
//     for readability. Encode already skips zero-valued, non-pointer
//     fields unconditionally, and Decode already skips absent JSON keys
//     unconditionally, so this modifier has no additional effect — it
//     documents the existing behavior rather than changing it.
//
// Unrecognized modifiers are ignored, so new ones can be introduced later
// without breaking existing tags.
//
// See docs/design.md §6 ("Read (X) vs. write (XOptions) structs: when to
// merge") for when a resource's read and write shapes are close enough to
// share one struct using these modifiers, versus when they should stay
// separate types.
func parseTag(tag string) (name string, readonly, writeonly, ok bool) {
	if tag == "" || tag == "-" {
		return "", false, false, false
	}

	parts := strings.Split(tag, ",")
	name = parts[0]

	for _, mod := range parts[1:] {
		switch mod {
		case "readonly":
			readonly = true
		case "writeonly":
			writeonly = true
		}
	}

	return name, readonly, writeonly, true
}
