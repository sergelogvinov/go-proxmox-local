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

package qemu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/sergelogvinov/go-proxmox-local/internal/params"
	restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

// This file is this module's own copy of go-proxmox-rest's (unexported)
// nodes/qemu decodeConfig/encodeConfig, since go-proxmox-rest does not
// export them (docs/design.md §6.4 proposed it; that change was not
// taken — see §12 Phase 2). It reaches the same result by driving
// internal/params.Decode/Encode over the shared restqemu.Config type and
// calling each indexed hardware type's own exported UnmarshalJSON/String
// methods (Net, Drive, HostPCI, ...) for the property-string entries —
// no access to go-proxmox-rest's unexported internals is required.

// indexedPrefixes are the numerically-suffixed VM config families
// (net0, ide2, hostpci3, ...) Config models as map[int]T instead of one
// field per index. A scalar field that merely happens to end in a digit
// (efidisk0, tpmstate0, rng0, audio0, smbios1) is not in this set and is
// decoded/encoded as an ordinary named field instead.
var indexedPrefixes = map[string]bool{
	"net":      true,
	"ide":      true,
	"sata":     true,
	"scsi":     true,
	"virtio":   true,
	"virtiofs": true,
	"unused":   true,
	"usb":      true,
	"hostpci":  true,
	"serial":   true,
	"parallel": true,
	"ipconfig": true,
	"numa":     true,
}

// indexedKeyRe splits a config key into its alphabetic prefix and
// trailing numeric index, e.g. "hostpci3" -> ("hostpci", "3").
var indexedKeyRe = regexp.MustCompile(`^([a-z]+)(\d+)$`)

// splitIndexedKey reports whether key is one of Proxmox's numerically
// suffixed VM config families, returning the family prefix and index if
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

// decodeIndexedValue JSON-quotes v and unmarshals it into a T via T's own
// UnmarshalJSON — the same property-string decoding go-proxmox-rest's
// indexed hardware types already implement, reused here so a config read
// from disk decodes each entry identically to one fetched over the REST
// API.
func decodeIndexedValue[T any](v string) (T, error) {
	var out T

	u, ok := any(&out).(json.Unmarshaler)
	if !ok {
		return out, fmt.Errorf("qemu: %T does not implement json.Unmarshaler", out)
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

// setIndexedDrive parses v as a drive property string and stores it at
// (*m)[index], allocating *m on first use.
func setIndexedDrive(m *map[int]restqemu.Drive, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.Drive](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.Drive{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedNet parses v as a netN property string and stores it at
// (*m)[index], allocating *m on first use. Net.UnmarshalJSON (rather
// than a bare property-string unmarshal) is required here: the
// model=macaddr alias (e.g. "virtio=AA:BB:...") is a dynamic key that
// only Net's own parsing logic recognizes.
func setIndexedNet(m *map[int]restqemu.Net, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.Net](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.Net{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedNUMA parses v as a NUMA property string and stores it at
// (*m)[index], allocating *m on first use.
func setIndexedNUMA(m *map[int]restqemu.NUMA, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.NUMA](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.NUMA{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedHostPCI parses v as a hostpciN property string and stores it
// at (*m)[index], allocating *m on first use.
func setIndexedHostPCI(m *map[int]restqemu.HostPCI, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.HostPCI](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.HostPCI{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedUSB parses v as a usbN property string and stores it at
// (*m)[index], allocating *m on first use.
func setIndexedUSB(m *map[int]restqemu.USB, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.USB](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.USB{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedIPConfig parses v as an ipconfigN property string and stores
// it at (*m)[index], allocating *m on first use.
func setIndexedIPConfig(m *map[int]restqemu.IPConfig, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.IPConfig](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.IPConfig{}
	}

	(*m)[index] = val

	return nil
}

// setIndexedVirtioFS parses v as a virtiofsN property string and stores it
// at (*m)[index], allocating *m on first use.
func setIndexedVirtioFS(m *map[int]restqemu.VirtioFS, index int, v string) error {
	val, err := decodeIndexedValue[restqemu.VirtioFS](v)
	if err != nil {
		return err
	}

	if *m == nil {
		*m = map[int]restqemu.VirtioFS{}
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
	case "ide":
		return setIndexedDrive(&cfg.IDE, index, v)
	case "sata":
		return setIndexedDrive(&cfg.SATA, index, v)
	case "scsi":
		return setIndexedDrive(&cfg.SCSI, index, v)
	case "virtio":
		return setIndexedDrive(&cfg.VirtIO, index, v)
	case "virtiofs":
		return setIndexedVirtioFS(&cfg.VirtioFS, index, v)
	case "unused":
		setIndexed(&cfg.Unused, index, v)
	case "usb":
		return setIndexedUSB(&cfg.USB, index, v)
	case "hostpci":
		return setIndexedHostPCI(&cfg.HostPCI, index, v)
	case "serial":
		setIndexed(&cfg.Serial, index, v)
	case "ipconfig":
		return setIndexedIPConfig(&cfg.IPConfig, index, v)
	case "numa":
		return setIndexedNUMA(&cfg.NUMA, index, v)
	}

	return nil
}

// addIndexed adds one "prefix+index" entry to p per key in m.
func addIndexed(p map[string]string, prefix string, m map[int]string) {
	for idx, v := range m {
		p[prefix+strconv.Itoa(idx)] = v
	}
}

// addIndexedDrive adds one "prefix+index" entry to p per key in m,
// serializing each drive back to its property string.
func addIndexedDrive(p map[string]string, prefix string, m map[int]restqemu.Drive) {
	for idx, v := range m {
		p[prefix+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedNet adds one "net<index>" entry to p per key in m,
// serializing each network interface back to its property string.
func addIndexedNet(p map[string]string, m map[int]restqemu.Net) {
	for idx, v := range m {
		p["net"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedNUMA adds one "numa<index>" entry to p per key in m,
// serializing each NUMA node back to its property string.
func addIndexedNUMA(p map[string]string, m map[int]restqemu.NUMA) {
	for idx, v := range m {
		p["numa"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedHostPCI adds one "hostpci<index>" entry to p per key in m,
// serializing each passthrough device back to its property string.
func addIndexedHostPCI(p map[string]string, m map[int]restqemu.HostPCI) {
	for idx, v := range m {
		p["hostpci"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedUSB adds one "usb<index>" entry to p per key in m,
// serializing each USB device back to its property string.
func addIndexedUSB(p map[string]string, m map[int]restqemu.USB) {
	for idx, v := range m {
		p["usb"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedIPConfig adds one "ipconfig<index>" entry to p per key in m,
// serializing each per-NIC IP configuration back to its property string.
func addIndexedIPConfig(p map[string]string, m map[int]restqemu.IPConfig) {
	for idx, v := range m {
		p["ipconfig"+strconv.Itoa(idx)] = v.String()
	}
}

// addIndexedVirtioFS adds one "virtiofs<index>" entry to p per key in m,
// serializing each virtiofs share back to its property string.
func addIndexedVirtioFS(p map[string]string, m map[int]restqemu.VirtioFS) {
	for idx, v := range m {
		p["virtiofs"+strconv.Itoa(idx)] = v.String()
	}
}

// DecodeConfig decodes a VM configuration from the "key -> raw string
// value" shape a parsed PVE config file (conf.File.Current.Map()) or
// `qm config <vmid>` produces. It splits raw's numerically-suffixed
// hardware keys into Config's map[int]T fields, then decodes everything
// else (the ordinary named fields) through internal/params.Decode.
func DecodeConfig(raw map[string]string) (*Config, error) {
	cfg := &Config{}
	scalar := make(map[string]json.RawMessage, len(raw))

	for key, val := range raw {
		prefix, index, ok := splitIndexedKey(key)
		if !ok {
			b, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("qemu: config field %s: %w", key, err)
			}

			scalar[key] = b

			continue
		}

		if err := setIndexedField(cfg, prefix, index, val); err != nil {
			return nil, fmt.Errorf("qemu: config field %s: %w", key, err)
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
// populated map[int]T field — the map[string]string form `qm
// set`/`qm create` expect.
func EncodeConfig(cfg *Config) (map[string]string, error) {
	if cfg == nil {
		return nil, fmt.Errorf("qemu: config is required")
	}

	p, err := params.Encode(cfg)
	if err != nil {
		return nil, err
	}

	addIndexedNet(p, cfg.Net)
	addIndexedDrive(p, "ide", cfg.IDE)
	addIndexedDrive(p, "sata", cfg.SATA)
	addIndexedDrive(p, "scsi", cfg.SCSI)
	addIndexedDrive(p, "virtio", cfg.VirtIO)
	addIndexedVirtioFS(p, cfg.VirtioFS)
	addIndexed(p, "unused", cfg.Unused)
	addIndexedUSB(p, cfg.USB)
	addIndexedHostPCI(p, cfg.HostPCI)
	addIndexed(p, "serial", cfg.Serial)
	addIndexedIPConfig(p, cfg.IPConfig)
	addIndexedNUMA(p, cfg.NUMA)

	return p, nil
}
