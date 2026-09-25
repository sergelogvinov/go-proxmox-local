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

import restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"

// Config is go-proxmox-rest's typed QEMU guest configuration, reused
// verbatim rather than redefined here (docs/design.md §6): this module
// owns the config *file* format (see the conf package), go-proxmox-rest
// owns the *type*. A *Config obtained from Get/List — read from disk via
// conf.Parse + DecodeConfig — is the identical Go type as one fetched
// over the REST API, so provider code can be written once against
// either source.
//
// This replaces the pre-Phase-2 local Config struct (16 scalar fields,
// ten explicit HostPCI0…HostPCI9 fields, YAML-tagged) — see
// docs/design.md §2 for the defects that struct had and this fixes.
type Config = restqemu.Config

// These are the Config field types a caller building or inspecting one
// typically needs to name directly (a NUMA pinning entry, the CPU/Memory
// settings, ...). Aliased here too, so a consumer never needs its own
// import of go-proxmox-rest just to construct a *Config literal.

// CPU is go-proxmox-rest's emulated-CPU-type/flags property (Config.CPU).
type CPU = restqemu.CPU

// HostPCI is go-proxmox-rest's PCI(e) passthrough entry (Config.HostPCI).
type HostPCI = restqemu.HostPCI

// Memory is go-proxmox-rest's guest memory configuration (Config.Memory).
type Memory = restqemu.Memory

// NUMA is go-proxmox-rest's per-NUMA-node CPU/memory pinning entry
// (Config.NUMA).
type NUMA = restqemu.NUMA

// Tags is go-proxmox-rest's guest tag list (Config.Tags).
type Tags = restqemu.Tags
