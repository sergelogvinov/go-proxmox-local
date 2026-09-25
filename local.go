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

package local

import (
	"context"
	"path/filepath"

	"github.com/sergelogvinov/go-proxmox-local/cluster"
	"github.com/sergelogvinov/go-proxmox-local/lxc"
	"github.com/sergelogvinov/go-proxmox-local/qemu"
	"github.com/sergelogvinov/go-proxmox-local/storage"
)

// Client is the entry point for driving a Proxmox VE hypervisor from the
// node itself. Obtain resource-scoped handles from its Qemu()/Cluster()
// methods; Client itself carries only the shared configuration and the
// command-execution seam (see Run, ConfigDir, RunDir), which the cluster
// and qemu subpackages consume through their own Env interfaces, so that
// neither needs to import this package (which would cycle back to them).
type Client struct {
	cfg config
}

// New returns a new Client. The default root is "/", i.e. this node's
// own filesystem; WithRoot points every path below it at an alternate
// root (a temp directory in tests).
func New(opts ...Option) (*Client, error) {
	cfg := config{root: "/"}

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.configDir == "" {
		cfg.configDir = filepath.Join(cfg.root, "etc/pve")
	}

	if cfg.runDir == "" {
		cfg.runDir = filepath.Join(cfg.root, "run/qemu-server")
	}

	if cfg.runner == nil {
		cfg.runner = newCommandRunner(cfg.timeout, cfg.dryRun, cfg.logger)
	}

	return &Client{cfg: cfg}, nil
}

// Qemu returns a handle for QEMU guest configuration and lifecycle
// operations.
func (c *Client) Qemu() *qemu.Client {
	return qemu.New(c)
}

// Cluster returns a handle for pmxcfs and cluster-scope operations.
func (c *Client) Cluster() *cluster.Client {
	return cluster.New(c)
}

// LXC returns a handle for LXC container configuration and lifecycle
// operations.
func (c *Client) LXC() *lxc.Client {
	return lxc.New(c)
}

// Storage returns a handle for local storage inspection.
func (c *Client) Storage() *storage.Client {
	return storage.New(c)
}

// Run executes a PVE CLI tool (pct, pvecm, pvesh, pvesm, or qm) through
// the configured Runner. It is exported so the cluster, qemu, lxc and
// storage subpackages can invoke it via their own Env interfaces without
// importing this package — see the package doc comment on Client.
func (c *Client) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return c.cfg.runner.Run(ctx, name, args...)
}

// ConfigDir returns the pmxcfs mount root (default "/etc/pve", or
// "<root>/etc/pve" under WithRoot).
func (c *Client) ConfigDir() string {
	return c.cfg.configDir
}

// RunDir returns the directory holding running guests' pid/vnc/qmp
// sockets (default "/run/qemu-server", or "<root>/run/qemu-server" under
// WithRoot).
func (c *Client) RunDir() string {
	return c.cfg.runDir
}
