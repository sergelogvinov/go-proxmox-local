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

// Package lxc provides LXC container configuration read access and
// lifecycle operations (create/update/delete via `pct`) on the local
// hypervisor. Its shape mirrors the qemu package's — reads go straight
// to the filesystem via the conf package, writes go through `pct`,
// encoded by this package's own DecodeConfig/EncodeConfig (config.go).
package lxc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sergelogvinov/go-proxmox-local/conf"
	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
)

// Env is the subset of the root client this package needs — keeping this
// package decoupled from the root package (no import cycle back to it,
// since the root package imports this one to wire up Client.LXC()).
// *local.Client satisfies it.
type Env interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
	ConfigDir() string
}

// Client provides LXC container configuration read access and lifecycle
// operations.
type Client struct {
	env Env
}

// New returns a new lxc Client backed by the given root client.
func New(e Env) *Client {
	return &Client{env: e}
}

// ConfigFile reads and parses the LXC container's raw config file for
// the given VM id, returning the parsed sections, key order and digest —
// the escape hatch for callers that need the raw config rather than the
// typed Config. Get is built on top of this.
func (c *Client) ConfigFile(_ context.Context, vmid int) (*conf.File, error) {
	path := c.configPath(vmid)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("vmid %d: %w", vmid, errdefs.ErrNotFound)
		}

		return nil, fmt.Errorf("failed to read CT config file %s: %w", path, err)
	}

	file, err := conf.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse CT config for CT %d: %w", vmid, err)
	}

	return file, nil
}

// Get reads and parses the LXC container config file for the given VM id
// directly from the configured config directory. The returned *Config is
// decoded by the same logic go-proxmox-rest's own REST Client.Config
// uses (see config.go), so a *Config read from disk and one fetched over
// the API are the identical Go value for the identical container.
func (c *Client) Get(ctx context.Context, vmid int) (*Config, error) {
	file, err := c.ConfigFile(ctx, vmid)
	if err != nil {
		return nil, err
	}

	cfg, err := DecodeConfig(file.Current.Map())
	if err != nil {
		return nil, fmt.Errorf("failed to decode CT config for CT %d: %w", vmid, err)
	}

	// PVE stores the description as '#' comment lines, not a
	// "description:" key — conf.Parse already pulled it out of the raw
	// section; DecodeConfig never saw it.
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

// ListFilter filters the entries List returns. Hostname is applied
// client-side after each config is decoded, same as Match.
type ListFilter struct {
	// Hostname restricts entries to containers with this exact
	// Config.Hostname. Empty disables the filter.
	Hostname string

	// Match, when set, is evaluated last for each config that passed
	// Hostname above; the entry is kept only if Match returns true. An
	// error return aborts List, and that error is returned to the
	// caller.
	Match func(*Config) (bool, error)
}

// List scans every LXC container config file in the configured config
// directory and returns the id/config of every container matched by
// filter. A zero ListFilter matches every container. Nothing matching is
// not an error: it returns an empty slice. A config that fails to read
// or parse is skipped rather than aborting the scan.
func (c *Client) List(ctx context.Context, filter ListFilter) ([]Guest, error) {
	dir := filepath.Join(c.env.ConfigDir(), "lxc")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read lxc directory: %w", err)
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
			continue // skip containers that can't be read
		}

		if filter.Hostname != "" && cfg.Hostname != filter.Hostname {
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

// Create creates a new container with the given id from ostemplate (an
// OS template volume, e.g. "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst")
// via `pct create`, encoded by EncodeConfig.
func (c *Client) Create(ctx context.Context, vmid int, ostemplate string, cfg *Config) error {
	options, err := encodeForWrite(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode CT %d config: %w", vmid, err)
	}

	args := append([]string{"create", strconv.Itoa(vmid), ostemplate}, flagArgs(options)...)

	if _, err := c.env.Run(ctx, "pct", args...); err != nil {
		return fmt.Errorf("failed to create CT %d: %w", vmid, err)
	}

	return nil
}

// Delete destroys the container with the given id via `pct destroy`.
func (c *Client) Delete(ctx context.Context, vmid int) error {
	if _, err := c.env.Run(ctx, "pct", "destroy", strconv.Itoa(vmid)); err != nil {
		return fmt.Errorf("failed to delete CT %d: %w", vmid, err)
	}

	return nil
}

// Update applies cfg to an existing container via `pct set`, computed as
// the diff between cfg and the container's current on-disk config —
// string against string, both sides encoded through EncodeConfig, the
// same approach qemu.Client.Update uses (see its doc comment for why).
// If nothing differs, Update returns without invoking pct. Otherwise the
// write carries the current config's digest.
func (c *Client) Update(ctx context.Context, vmid int, cfg *Config) error {
	file, err := c.ConfigFile(ctx, vmid)
	if err != nil {
		return err
	}

	current, err := DecodeConfig(file.Current.Map())
	if err != nil {
		return fmt.Errorf("failed to decode CT config for CT %d: %w", vmid, err)
	}

	current.Description = file.Description

	currentParams, err := EncodeConfig(current)
	if err != nil {
		return fmt.Errorf("failed to encode current CT %d config: %w", vmid, err)
	}

	desiredParams, err := encodeForWrite(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode desired CT %d config: %w", vmid, err)
	}

	changed := filterChangedOptions(currentParams, desiredParams)
	if len(changed) == 0 {
		return nil
	}

	changed["digest"] = file.Digest()

	args := append([]string{"set", strconv.Itoa(vmid)}, flagArgs(changed)...)

	if _, err := c.env.Run(ctx, "pct", args...); err != nil {
		return fmt.Errorf("failed to update CT %d: %w", vmid, err)
	}

	return nil
}

// configPath returns the on-disk config file path for the given VM id.
func (c *Client) configPath(vmid int) string {
	return filepath.Join(c.env.ConfigDir(), "lxc", fmt.Sprintf("%d.conf", vmid))
}

// encodeForWrite encodes cfg via EncodeConfig with Digest always cleared
// first, regardless of what the caller's cfg carries — see
// qemu.encodeForWrite's doc comment for why.
func encodeForWrite(cfg *Config) (map[string]string, error) {
	desired := *cfg
	desired.Digest = ""

	return EncodeConfig(&desired)
}

// flagArgs builds the `--key value ...` argument list for options, in
// sorted key order — options comes from a Go map, so sorting is what
// makes the resulting argv deterministic.
func flagArgs(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	args := make([]string, 0, 2*len(options))
	for _, key := range keys {
		args = append(args, "--"+key, options[key])
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
