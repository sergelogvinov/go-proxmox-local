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

	"github.com/sergelogvinov/go-proxmox-local/internal/params"
	restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

// DecodeConfig decodes a VM configuration from the "key -> raw string
// value" shape a parsed PVE config file (conf.File.Current.Map()) or
// `qm config <vmid>` produces, through internal/params.Decode — the same
// decoder go-proxmox-rest uses for a GET response, indexed families
// (net0, scsi1, ...) included.
func DecodeConfig(raw map[string]string) (*Config, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("qemu: config: %w", err)
	}

	cfg := &Config{}
	if err := params.Decode(b, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// EncodeConfig encodes cfg into the map[string]string form `qm set`/`qm
// create` expect, via go-proxmox-rest's own encoder.
func EncodeConfig(cfg *Config) (map[string]string, error) {
	return restqemu.EncodeConfig(cfg)
}
