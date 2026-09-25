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
	"context"
	"fmt"
	"strconv"
)

// Start starts the QEMU guest with the given id via `qm start`.
func (c *Client) Start(ctx context.Context, vmid int) error {
	if _, err := c.env.Run(ctx, "qm", "start", strconv.Itoa(vmid)); err != nil {
		return fmt.Errorf("failed to start VM %d: %w", vmid, err)
	}

	return nil
}

// Stop stops the QEMU guest with the given id via `qm stop` — an
// immediate power-off (like pulling the power plug), not a graceful ACPI
// shutdown.
func (c *Client) Stop(ctx context.Context, vmid int) error {
	if _, err := c.env.Run(ctx, "qm", "stop", strconv.Itoa(vmid)); err != nil {
		return fmt.Errorf("failed to stop VM %d: %w", vmid, err)
	}

	return nil
}

// Reboot reboots the QEMU guest with the given id via `qm reboot` — a
// graceful ACPI reboot (shutdown followed by start).
func (c *Client) Reboot(ctx context.Context, vmid int) error {
	if _, err := c.env.Run(ctx, "qm", "reboot", strconv.Itoa(vmid)); err != nil {
		return fmt.Errorf("failed to reboot VM %d: %w", vmid, err)
	}

	return nil
}
