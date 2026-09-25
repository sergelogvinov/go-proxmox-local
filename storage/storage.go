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

// Package storage provides local storage inspection via `pvesm`. Unlike
// qemu/lxc, there is no config file to read directly here — pvesm is the
// only source for a storage's live usage and content, so, unlike the
// rest of this module, every call in this package forks a process (see
// docs/design.md §7.1's "except as an explicit fallback" carve-out,
// which this package is squarely in — there is no local file to read
// instead of one).
package storage

import (
	"context"
	"fmt"

	"github.com/sergelogvinov/go-proxmox-local/internal/params"
	reststorage "github.com/sergelogvinov/go-proxmox-rest/nodes/storage"
)

// Storage is go-proxmox-rest's typed node storage status (space usage,
// enabled/active state), reused verbatim (docs/design.md §6) rather than
// redefined here. Its `url` tags are shaped for GET /nodes/{node}/storage;
// `pvesm status --output-format json`'s fields overlap it closely enough
// to decode into it via internal/params.Decode (which — like the field
// names — this package borrows from go-proxmox-rest, since Active/Enabled
// arrive from pvesm as JSON numbers, not real JSON bools, and stdlib
// encoding/json cannot coerce that on its own). This has not been
// verified against a live cluster — treat unfamiliar/absent fields as
// expected.
type Storage = reststorage.Storage

// Volume is go-proxmox-rest's typed storage volume entry, analogous to
// Storage above for `pvesm list`'s output.
type Volume = reststorage.Volume

// Env is the subset of the root client this package needs — keeping this
// package decoupled from the root package (no import cycle back to it,
// since the root package imports this one to wire up Client.Storage()).
// *local.Client satisfies it.
type Env interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}

// Client provides local storage inspection.
type Client struct {
	env Env
}

// New returns a new storage Client backed by the given root client.
func New(e Env) *Client {
	return &Client{env: e}
}

// Status lists every storage configured on this node and its current
// usage, via `pvesm status --output-format json`.
func (c *Client) Status(ctx context.Context) ([]Storage, error) {
	out, err := c.env.Run(ctx, "pvesm", "status", "--output-format", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get storage status: %w", err)
	}

	var statuses []Storage
	if err := params.Decode(out, &statuses); err != nil {
		return nil, fmt.Errorf("failed to parse pvesm status output: %w", err)
	}

	return statuses, nil
}

// List lists storageID's content, via
// `pvesm list <storageID> --output-format json`.
func (c *Client) List(ctx context.Context, storageID string) ([]Volume, error) {
	out, err := c.env.Run(ctx, "pvesm", "list", storageID, "--output-format", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to list storage %s: %w", storageID, err)
	}

	var volumes []Volume
	if err := params.Decode(out, &volumes); err != nil {
		return nil, fmt.Errorf("failed to parse pvesm list output for storage %s: %w", storageID, err)
	}

	return volumes, nil
}
