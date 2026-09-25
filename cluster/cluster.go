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

// Package cluster provides pmxcfs and cluster-scope operations: quorum
// state and the next available VM/CT id. Phase 1 (see docs/design.md
// §12) still shells out to pvecm/pvesh for both; a later phase reads
// /etc/pve/.members and /etc/pve/.vmlist directly instead, falling back
// to these same commands only when a pmxcfs file is absent or
// unparseable.
package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Env is the subset of the root client this package needs — keeping this
// package decoupled from the root package (no import cycle back to it,
// since the root package imports this one to wire up Client.Cluster()).
// *local.Client satisfies it.
type Env interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}

// Client provides pmxcfs and cluster-scope operations.
type Client struct {
	env Env
}

// New returns a new cluster Client backed by the given root client.
func New(e Env) *Client {
	return &Client{env: e}
}

// Quorate reports whether the local Proxmox cluster node currently has
// quorum, per `pvecm status`.
func (c *Client) Quorate(ctx context.Context) (bool, error) {
	output, err := c.env.Run(ctx, "pvecm", "status")
	if err != nil {
		return false, fmt.Errorf("failed to get proxmox cluster status: %w", err)
	}

	return parseQuorate(string(output))
}

// parseQuorate extracts the "Quorate" value from `pvecm status` output.
func parseQuorate(output string) (bool, error) {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)

		if !strings.HasPrefix(line, "Quorate") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		value := strings.TrimSpace(parts[1])

		return strings.EqualFold(value, "yes") || value == "1", nil
	}

	return false, fmt.Errorf("could not determine cluster quorum status from pvecm output")
}

// NextID asks the local hypervisor for the next available VM/CT id in
// the cluster, via `pvesh get /cluster/nextid`.
func (c *Client) NextID(ctx context.Context) (int, error) {
	output, err := c.env.Run(ctx, "pvesh", "get", "/cluster/nextid")
	if err != nil {
		return 0, fmt.Errorf("failed to get next VM ID: %w", err)
	}

	vmid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse next VM ID %q: %w", string(output), err)
	}

	return vmid, nil
}
