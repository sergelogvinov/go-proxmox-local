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
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

// VMStatus is go-proxmox-rest's QEMU process running state (Guest.Status,
// ListFilter.Status).
type VMStatus = restqemu.VMStatus

const (
	// VMStatusStopped is a guest with no live QEMU process.
	VMStatusStopped = restqemu.VMStatusStopped
	// VMStatusRunning is a guest whose QEMU process is alive.
	VMStatusRunning = restqemu.VMStatusRunning
)

// status reports whether guest vmid runs on this node and as which
// process: its pid file in the run dir holds a positive PID, and that
// process's command line is the QEMU of this guest (an "-id" argument
// followed by vmid). A pid file left behind with a PID since reused by
// another process therefore reads as stopped. Anything unreadable reads as
// stopped, with PID 0.
func (c *Client) status(vmid int) (VMStatus, int) {
	data, err := os.ReadFile(filepath.Join(c.env.RunDir(), strconv.Itoa(vmid)+".pid"))
	if err != nil {
		return VMStatusStopped, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return VMStatusStopped, 0
	}

	cmdline, err := os.ReadFile(filepath.Join(c.env.ProcDir(), strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return VMStatusStopped, 0
	}

	args := strings.Split(string(cmdline), "\x00")

	idx := slices.Index(args, "-id")
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != strconv.Itoa(vmid) {
		return VMStatusStopped, 0
	}

	return VMStatusRunning, pid
}

// ListError is returned by List, alongside the guests it did read, when
// some guests' config files could not be read (an I/O or pmxcfs error, as
// opposed to a file that is gone or does not parse). Their status is
// unknown: a caller must not take their absence from the result to mean
// they are not there, or not running.
type ListError struct {
	// Skipped maps each VM id whose config could not be read to why.
	Skipped map[int]error
}

func (e *ListError) Error() string {
	ids := make([]string, 0, len(e.Skipped))
	for _, vmid := range slices.Sorted(maps.Keys(e.Skipped)) {
		ids = append(ids, strconv.Itoa(vmid))
	}

	return fmt.Sprintf("failed to read the config of VMs %s", strings.Join(ids, ", "))
}

// Unwrap returns each skipped guest's error, so errors.Is/As see them.
func (e *ListError) Unwrap() []error {
	errs := make([]error, 0, len(e.Skipped))
	for _, vmid := range slices.Sorted(maps.Keys(e.Skipped)) {
		errs = append(errs, e.Skipped[vmid])
	}

	return errs
}
