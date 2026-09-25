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

package lxc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/sergelogvinov/go-proxmox-local/internal/params"
	restlxc "github.com/sergelogvinov/go-proxmox-rest/nodes/lxc"
)

// This file mirrors qemu/config.go: this module's own copy of
// go-proxmox-rest's (unexported) nodes/lxc decodeConfig/encodeConfig,
// reimplemented against the shared restlxc.Config type and each indexed
// type's own exported UnmarshalJSON/String methods (Net, MountPoint) —
// see qemu/config.go's file comment for why this is a copy rather than
// an upstream export.

// indexedPrefixes are the numerically-suffixed CT config families
// (net0, mp3, unused12, ...) Config models as map[int]T instead of one
// field per index.
var indexedPrefixes = map[string]bool{
	"net":    true,
	"mp":     true,
	"unused": true,
}

// indexedKeyRe splits a config key into its alphabetic prefix and
// trailing numeric index, e.g. "mp3" -> ("mp", "3").
var indexedKeyRe = regexp.MustCompile(`^([a-z]+)(\d+)$`)

// splitIndexedKey reports whether key is one of Proxmox's numerically
// suffixed CT config families, returning the family prefix and index if
// so.
func splitIndexedKey(key string) (prefix string, index int, ok bool) {
	m := indexedKeyRe.FindStringSubmatch(key)
	if m == nil || !indexedPrefixes[m[1]] {
		return "", 0, false
	}

	idx, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}

	return m[1], idx, true
}

// decodeIndexedValue JSON-quotes v and unmarshals it into a T via T's
// own UnmarshalJSON.
func decodeIndexedValue[T any](v string) (T, error) {
	var out T

	u, ok := any(&out).(json.Unmarshaler)
	if !ok {
		return out, fmt.Errorf("lxc: %T does not implement json.Unmarshaler", out)
	}

	b, err := json.Marshal(v)
	if err != nil {
		return out, err
	}

	if err := u.UnmarshalJSON(b); err != nil {
		return out, err
	}

	return out, nil
}

// setIndexed sets (*m)[index] = v, allocating *m on first use.
func setIndexed(m *map[int]string, index int, v string) {
	if *m == nil {
		*m = map[int]string{}
	}

	(*m)[index] = v
}

// setIndexedNet parses v as a netN property string and stores it at
// (*m)[index], allocating *m on first use.
func setIndexedNet(m *map[int]restlxc.Net, index int, v string) error {
	val, err := decodeIndexedValue[restlxc.Net](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restlxc.Net{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedMountPoint parses v as an mpN property string and stores it
// at (*m)[index], allocating *m on first use.
func setIndexedMountPoint(m *map[int]restlxc.MountPoint, index int, v string) error {
	val, err := decodeIndexedValue[restlxc.MountPoint](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restlxc.MountPoint{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedField assigns v into cfg's map field for the given family
// prefix, allocating the map on first use.
func setIndexedField(cfg *Config, prefix string, index int, v string) error {
	switch prefix {
	case "net":
		return setIndexedNet(&cfg.Net, index, v)
	case "mp":
		return setIndexedMountPoint(&cfg.MP, index, v)
	case "unused":
		setIndexed(&cfg.Unused, index, v)
	}

	return nil
}

// addIndexed adds one "prefix+index" entry to p per key in m.
func addIndexed(p map[string]string, prefix string, m map[int]string) {
	for idx, v := range m {
		p[prefix+strconv.Itoa(idx)] = v
	}
}

// addIndexedNet adds one "net<index>" entry to p per key in m,
// serializing each network interface back to its property string.
func addIndexedNet(p map[string]string, m map[int]restlxc.Net) {
	for idx, v := range m {
		p["net"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedMountPoint adds one "mp<index>" entry to p per key in m,
// serializing each mount point back to its property string.
func addIndexedMountPoint(p map[string]string, m map[int]restlxc.MountPoint) {
	for idx, v := range m {
		p["mp"+strconv.Itoa(idx)] = v.String()
	}
}

// DecodeConfig decodes a container configuration from the "key -> raw
// string value" shape a parsed PVE config file (conf.File.Current.Map())
// or `pct config <vmid>` produces.
func DecodeConfig(raw map[string]string) (*Config, error) {
	cfg := &Config{}
	scalar := make(map[string]json.RawMessage, len(raw))

	for key, val := range raw {
		prefix, index, ok := splitIndexedKey(key)
		if !ok {
			b, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("lxc: config field %s: %w", key, err)
			}

			scalar[key] = b

			continue
		}

		if err := setIndexedField(cfg, prefix, index, val); err != nil {
			return nil, fmt.Errorf("lxc: config field %s: %w", key, err)
		}
	}

	b, err := json.Marshal(scalar)
	if err != nil {
		return nil, err
	}

	if err := params.Decode(b, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// EncodeConfig encodes cfg's ordinary named fields through
// internal/params.Encode, then adds one "prefix+index" entry per
// populated map[int]T field — the map[string]string form `pct
// set`/`pct create` expect.
func EncodeConfig(cfg *Config) (map[string]string, error) {
	if cfg == nil {
		return nil, fmt.Errorf("lxc: config is required")
	}

	p, err := params.Encode(cfg)
	if err != nil {
		return nil, err
	}

	addIndexedNet(p, cfg.Net)
	addIndexedMountPoint(p, cfg.MP)
	addIndexed(p, "unused", cfg.Unused)

	return p, nil
}
