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

// Package qemu provides QEMU guest configuration read access and
// lifecycle operations (create/update/delete via `qm`) on the local
// hypervisor. Reads go straight to the filesystem via the conf package;
// writes always go through `qm`, encoded by this package's own
// DecodeConfig/EncodeConfig (config.go) — see docs/design.md §7.
package qemu

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sergelogvinov/go-proxmox-local/conf"
	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
)

// Env is the subset of the root client this package needs — keeping this
// package decoupled from the root package (no import cycle back to it,
// since the root package imports this one to wire up Client.Qemu()).
// *local.Client satisfies it.
type Env interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
	ConfigDir() string
}

// Client provides QEMU guest configuration read access and lifecycle
// operations.
type Client struct {
	env Env
}

// New returns a new qemu Client backed by the given root client.
func New(e Env) *Client {
	return &Client{env: e}
}

// ConfigFile reads and parses the QEMU guest's raw config file for the
// given VM id, returning the parsed sections, key order and digest — the
// escape hatch for callers that need the raw config rather than the
// typed Config, e.g. an unmodelled key or a snapshot section. Get is
// built on top of this.
func (c *Client) ConfigFile(_ context.Context, vmid int) (*conf.File, error) {
	path := c.configPath(vmid)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("vmid %d: %w", vmid, errdefs.ErrNotFound)
		}

		return nil, fmt.Errorf("failed to read VM config file %s: %w", path, err)
	}

	file, err := conf.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse VM config for VM %d: %w", vmid, err)
	}

	return file, nil
}

// Get reads and parses the QEMU guest config file for the given VM id
// directly from the configured config directory — the local counterpart
// to go-proxmox-rest's Get(ctx, id) convention (pools.Get, storage.Get,
// nodes/network.Get, ...). The returned *Config is decoded by the exact
// same logic go-proxmox-rest's own REST Client.Config uses (see
// config.go), so a *Config read from disk and one fetched over the API
// are the identical Go value for the identical guest.
func (c *Client) Get(ctx context.Context, vmid int) (*Config, error) {
	file, err := c.ConfigFile(ctx, vmid)
	if err != nil {
		return nil, err
	}

	cfg, err := DecodeConfig(file.Current.Map())
	if err != nil {
		return nil, fmt.Errorf("failed to decode VM config for VM %d: %w", vmid, err)
	}

	// PVE stores the description as '#' comment lines, not a
	// "description:" key (docs/design.md §2.1) — conf.Parse already
	// pulled it out of the raw section; DecodeConfig never saw it.
	cfg.Description = file.Description
	cfg.Digest = file.Digest()

	return cfg, nil
}

// Guest pairs a VM id with the config found for it — List's result
// element, since a Config on its own does not carry the id it was read
// from.
type Guest struct {
	VMID   int
	Config *Config
}

// ListFilter filters the entries List returns. Name is applied
// client-side after each config is decoded, same as Match; there is no
// server-side filtering here, unlike go-proxmox-rest's
// cluster.ListFilter, since List is a directory scan, not an API call.
type ListFilter struct {
	// Name restricts entries to guests with this exact Config.Name. Empty
	// disables the filter.
	Name string

	// Match, when set, is evaluated last for each config that passed Name
	// above; the entry is kept only if Match returns true. Use it for
	// conditions List can't express directly. An error return aborts
	// List, and that error is returned to the caller.
	Match func(*Config) (bool, error)
}

// List scans every QEMU guest config file in the configured config
// directory and returns the id/config of every VM matched by filter. A
// zero ListFilter matches every VM. Nothing matching is not an error: it
// returns an empty slice. A config that fails to read or parse is
// skipped rather than aborting the scan.
func (c *Client) List(ctx context.Context, filter ListFilter) ([]Guest, error) {
	dir := filepath.Join(c.env.ConfigDir(), "qemu-server")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read qemu-server directory: %w", err)
	}

	var guests []Guest

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		vmidStr, ok := strings.CutSuffix(entry.Name(), ".conf")
		if !ok {
			continue
		}

		vmid, err := strconv.Atoi(vmidStr)
		if err != nil {
			continue // skip non-numeric filenames
		}

		cfg, err := c.Get(ctx, vmid)
		if err != nil {
			continue // skip VMs that can't be read
		}

		if filter.Name != "" && cfg.Name != filter.Name {
			continue
		}

		if filter.Match != nil {
			ok, err := filter.Match(cfg)
			if err != nil {
				return nil, err
			}

			if !ok {
				continue
			}
		}

		guests = append(guests, Guest{VMID: vmid, Config: cfg})
	}

	return guests, nil
}

// Create creates a new QEMU guest with the given id via `qm create`,
// encoded by EncodeConfig — the same encoder Update uses to build a
// `qm set` invocation.
func (c *Client) Create(ctx context.Context, vmid int, cfg *Config) error {
	options, err := encodeForWrite(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode VM %d config: %w", vmid, err)
	}

	if _, err := c.env.Run(ctx, "qm", commandArgs("create", vmid, options)...); err != nil {
		return fmt.Errorf("failed to create VM %d: %w", vmid, err)
	}

	return nil
}

// Delete destroys the QEMU guest with the given id via the local
// `qm destroy` CLI.
func (c *Client) Delete(ctx context.Context, vmid int) error {
	if _, err := c.env.Run(ctx, "qm", "destroy", strconv.Itoa(vmid)); err != nil {
		return fmt.Errorf("failed to delete VM %d: %w", vmid, err)
	}

	return nil
}

// Update applies cfg to an existing QEMU guest via `qm set`, computed as
// the diff between cfg and the guest's current on-disk config — string
// against string, both sides encoded through EncodeConfig, so the
// idempotency bug docs/design.md §2.4 describes (comparing a uint64
// against an int of the same value with ==) cannot recur: there is no
// longer a second, differently-typed representation of a value to
// compare against (§7.3). If nothing differs, Update returns without
// invoking qm. Otherwise the write carries the current config's digest
// (§7.4), so a concurrent qm set or web-UI edit aborts the write instead
// of silently overwriting it.
func (c *Client) Update(ctx context.Context, vmid int, cfg *Config) error {
	file, err := c.ConfigFile(ctx, vmid)
	if err != nil {
		return err
	}

	current, err := DecodeConfig(file.Current.Map())
	if err != nil {
		return fmt.Errorf("failed to decode VM config for VM %d: %w", vmid, err)
	}

	current.Description = file.Description

	currentParams, err := EncodeConfig(current)
	if err != nil {
		return fmt.Errorf("failed to encode current VM %d config: %w", vmid, err)
	}

	desiredParams, err := encodeForWrite(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode desired VM %d config: %w", vmid, err)
	}

	changed := filterChangedOptions(currentParams, desiredParams)
	if len(changed) == 0 {
		return nil
	}

	changed["digest"] = file.Digest()

	if _, err := c.env.Run(ctx, "qm", commandArgs("set", vmid, changed)...); err != nil {
		return fmt.Errorf("failed to update VM %d: %w", vmid, err)
	}

	return nil
}

// configPath returns the on-disk config file path for the given VM id.
func (c *Client) configPath(vmid int) string {
	return filepath.Join(c.env.ConfigDir(), "qemu-server", fmt.Sprintf("%d.conf", vmid))
}

// encodeForWrite encodes cfg via EncodeConfig with Digest always cleared
// first, regardless of what the caller's cfg carries: Create has no
// existing file to guard, and Update computes its own digest fresh from
// the config it just read (see Update's doc comment) rather than
// trusting a possibly-stale value on cfg.
func encodeForWrite(cfg *Config) (map[string]string, error) {
	desired := *cfg
	desired.Digest = ""

	return EncodeConfig(&desired)
}

// commandArgs builds the `qm <action> <vmid> --key value ...` argument
// list for the given options.
func commandArgs(action string, vmid int, options map[string]string) []string {
	args := make([]string, 0, 2+2*len(options))
	args = append(args, action, strconv.Itoa(vmid))

	for key, value := range options {
		args = append(args, "--"+key, value)
	}

	return args
}

// filterChangedOptions returns the subset of desired whose value differs
// from (or is absent from) current.
func filterChangedOptions(current, desired map[string]string) map[string]string {
	changed := make(map[string]string, len(desired))

	for key, value := range desired {
		if v, ok := current[key]; ok && v == value {
			continue
		}

		changed[key] = value
	}

	return changed
}
