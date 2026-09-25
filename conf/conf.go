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

// Package conf parses and formats the PVE section-config file format
// used by /etc/pve/qemu-server/<vmid>.conf (and its LXC/other
// counterparts): '#' description comment lines, "key: value" lines, a
// "[PENDING]" section, and "[<snapshot>]" sections.
//
// This package knows about lines, sections and comments — nothing about
// what a value *means*. It never interprets a value: every value is a
// string, always. That is deliberate (docs/design.md §6.1): a config
// value's meaning is go-proxmox-rest's nodes/qemu.Config's job, via
// DecodeConfig(Section.Map()); this package is what makes that handoff
// possible without an intermediate YAML decoder rewriting values it does
// not understand (see docs/design.md §2.3).
//
// Format exists for round-trip tests and for diffing/inspection — it is
// not a write path. All guest config writes go through `qm`, which owns
// locking, schema validation and pmxcfs replication (docs/design.md
// §7.2); this package never touches /etc/pve.
package conf

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"maps"
	"sort"
	"strings"
)

// Section preserves a PVE config section's key -> value mapping and the
// order keys appeared in the source file, so Format(Parse(x)) == x for
// any config PVE wrote (with the exception noted on File.Snapshots).
// Every value is the raw string PVE wrote; Section never interprets one.
type Section struct {
	keys   []string
	values map[string]string
}

// Map returns the section as a plain map — the handoff to
// go-proxmox-rest's nodes/qemu.DecodeConfig. A Go map does not preserve
// key order; Format uses Section's own internal order instead.
func (s Section) Map() map[string]string {
	out := make(map[string]string, len(s.values))
	maps.Copy(out, s.values)

	return out
}

// set appends key to the section (if new) and stores value, preserving
// first-seen key order. PVE never repeats a key within one section, so a
// duplicate here (malformed input) simply keeps its original position
// and takes the later value.
func (s *Section) set(key, value string) {
	if s.values == nil {
		s.values = make(map[string]string)
	}

	if _, exists := s.values[key]; !exists {
		s.keys = append(s.keys, key)
	}

	s.values[key] = value
}

// File is a parsed PVE section-config file.
type File struct {
	// Description is the guest's description. PVE stores it as
	// '#'-prefixed comment lines at the top of the file, not as a
	// "description:" key (docs/design.md §2.1) — this is those lines,
	// joined with "\n", with each line's leading "#" stripped.
	Description string
	// Current is the active configuration.
	Current Section
	// Pending is the "[PENDING]" section: changes queued but not yet
	// applied. Its zero value (no keys) means the file has no [PENDING]
	// section — every section is parsed rather than truncated at the
	// first one found (docs/design.md §2.2).
	Pending Section
	// Snapshots holds every "[<name>]" section, keyed by snapshot name.
	// A Go map cannot preserve the sections' original order in the file;
	// Format writes them back in sorted name order instead. Digest is
	// unaffected by this — see its doc comment.
	Snapshots map[string]Section

	// raw is the exact bytes Parse read, for a File that came from
	// Parse. Digest hashes these directly; see its doc comment.
	raw []byte
}

// Parse reads a PVE section-config file: '#' description comments at the
// top, "key: value" lines forming the active (Current) configuration, an
// optional "[PENDING]" section, and zero or more "[<snapshot>]" sections.
func Parse(r io.Reader) (*File, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("conf: read: %w", err)
	}

	f := &File{raw: raw}

	var (
		cur           Section
		target        *Section // &f.Current or &f.Pending; nil while cur belongs to a snapshot
		snapshotName  string
		descLines     []string
		inDescription = true
	)

	target = &f.Current

	flush := func() {
		if snapshotName != "" {
			if f.Snapshots == nil {
				f.Snapshots = make(map[string]Section)
			}

			f.Snapshots[snapshotName] = cur
		} else if target != nil {
			*target = cur
		}

		cur = Section{}
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}

		if inDescription {
			if rest, ok := strings.CutPrefix(line, "#"); ok {
				descLines = append(descLines, rest)
				continue
			}

			inDescription = false
		}

		if name, ok := sectionHeader(line); ok {
			flush()

			if name == "PENDING" {
				target = &f.Pending
				snapshotName = ""
			} else {
				target = nil
				snapshotName = name
			}

			continue
		}

		key, value, ok := splitKV(line)
		if !ok {
			continue
		}

		cur.set(key, value)
	}

	flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("conf: scan: %w", err)
	}

	f.Description = strings.Join(descLines, "\n")

	return f, nil
}

// sectionHeader reports whether line is a "[name]" section header.
func sectionHeader(line string) (name string, ok bool) {
	if len(line) < 2 || line[0] != '[' || line[len(line)-1] != ']' {
		return "", false
	}

	return line[1 : len(line)-1], true
}

// splitKV splits a "key: value" line on the first ':'. A single leading
// space in the value (the space PVE always writes after the colon) is
// trimmed; the rest of the value is kept verbatim, including any
// internal '#', ':' or leading/trailing content — conf never interprets
// a value (docs/design.md §2.3).
func splitKV(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}

	value = strings.TrimPrefix(value, " ")

	return key, value, true
}

// Format writes f back out in PVE's section-config format: the
// description as '#' lines, then Current, then "[PENDING]" if present,
// then every snapshot section in sorted name order (see Snapshots' doc
// comment on why sorted, not source, order).
func (f *File) Format(w io.Writer) error {
	bw := bufio.NewWriter(w)

	if f.Description != "" {
		for line := range strings.SplitSeq(f.Description, "\n") {
			if _, err := fmt.Fprintf(bw, "#%s\n", line); err != nil {
				return err
			}
		}
	}

	if err := writeSection(bw, f.Current); err != nil {
		return err
	}

	if len(f.Pending.keys) > 0 {
		if _, err := fmt.Fprintln(bw, "[PENDING]"); err != nil {
			return err
		}

		if err := writeSection(bw, f.Pending); err != nil {
			return err
		}
	}

	if len(f.Snapshots) > 0 {
		names := make([]string, 0, len(f.Snapshots))
		for name := range f.Snapshots {
			names = append(names, name)
		}

		sort.Strings(names)

		for _, name := range names {
			if _, err := fmt.Fprintf(bw, "[%s]\n", name); err != nil {
				return err
			}

			if err := writeSection(bw, f.Snapshots[name]); err != nil {
				return err
			}
		}
	}

	return bw.Flush()
}

// writeSection writes sec's keys, in their original order, as "key:
// value" lines.
func writeSection(w io.Writer, sec Section) error {
	for _, key := range sec.keys {
		if _, err := fmt.Fprintf(w, "%s: %s\n", key, sec.values[key]); err != nil {
			return err
		}
	}

	return nil
}
