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

import "strings"

// VfioPCIDevice is a vfio-pci passthrough device found on a running
// guest's QEMU command line.
type VfioPCIDevice struct {
	// ID is the hostpciN config key this device was attached as (e.g.
	// "hostpci0").
	ID string
	// HostAddress is the PCI host address (e.g. "0000:81:00.3").
	HostAddress string
}

// ParseVfioPCIDevices parses a running guest's QEMU command-line
// arguments (as split from /run/qemu-server or /proc/<pid>/cmdline) for
// vfio-pci passthrough devices, extracting each one's host address and
// hostpciN id. Ported from karpenter-provider-proxmox's
// pkg/utils/vmconfig.ParseVfioPciDevices; ProcessDiscovery reading pids
// off disk lands in a later phase (docs/design.md §12).
// Example argument: "-device vfio-pci,host=0000:81:00.3,id=hostpci0".
func ParseVfioPCIDevices(cmdlineArgs []string) []VfioPCIDevice {
	var devices []VfioPCIDevice

	for _, arg := range cmdlineArgs {
		if !strings.Contains(arg, "vfio-pci") || !strings.Contains(arg, "host=") || !strings.Contains(arg, "id=") {
			continue
		}

		var hostAddr, deviceID string

		for param := range strings.SplitSeq(arg, ",") {
			if v, ok := strings.CutPrefix(param, "host="); ok {
				hostAddr = v
			}

			if v, ok := strings.CutPrefix(param, "id="); ok {
				deviceID = v
			}

			if hostAddr != "" && deviceID != "" {
				break
			}
		}

		if hostAddr != "" && deviceID != "" {
			devices = append(devices, VfioPCIDevice{ID: deviceID, HostAddress: hostAddr})
		}
	}

	return devices
}
