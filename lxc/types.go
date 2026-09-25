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

import restlxc "github.com/sergelogvinov/go-proxmox-rest/nodes/lxc"

// Config is go-proxmox-rest's typed LXC container configuration, reused
// verbatim rather than redefined here (docs/design.md §6): this module
// owns the config *file* format (see the conf package), go-proxmox-rest
// owns the *type*. A *Config obtained from Get/List — read from disk via
// conf.Parse + DecodeConfig — is the identical Go type as one fetched
// over the REST API.
type Config = restlxc.Config

// These are the Config field types a caller building or inspecting one
// typically needs to name directly.

// Features is go-proxmox-rest's container feature-flags property
// (Config.Features).
type Features = restlxc.Features

// MountPoint is go-proxmox-rest's additional-mount-point entry
// (Config.MP).
type MountPoint = restlxc.MountPoint

// Net is go-proxmox-rest's container network interface entry
// (Config.Net).
type Net = restlxc.Net

// RootFS is go-proxmox-rest's container root mount point (Config.RootFS).
type RootFS = restlxc.RootFS
