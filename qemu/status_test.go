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
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProcess seeds env with a pid file for vmid holding pidFile, and a
// process pid whose command line is args.
func writeProcess(t *testing.T, env *fakeEnv, vmid int, pidFile string, pid int, args ...string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(env.runDir, strconv.Itoa(vmid)+".pid"), []byte(pidFile), 0o600))

	if len(args) == 0 {
		return
	}

	dir := filepath.Join(env.procDir, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600))
}

func vmids(guests []Guest) []int {
	ids := make([]int, 0, len(guests))
	for _, g := range guests {
		ids = append(ids, g.VMID)
	}

	return ids
}

func TestClientListStatus(t *testing.T) {
	c, env := newTestClient(t)

	for _, vmid := range []int{100, 101, 102, 103, 104, 105} {
		writeVMConfig(t, c, vmid, "name: vm"+strconv.Itoa(vmid)+"\n")
	}

	writeVMConfig(t, c, 106, "name: tmpl\ntemplate: 1\n")

	writeProcess(t, env, 100, "4100\n", 4100, "/usr/bin/kvm", "-id", "100", "-name", "vm100")
	// 101 has no pid file.
	writeProcess(t, env, 102, "4102\n", 4102, "/usr/bin/kvm", "-id", "999") // stale: PID reused by another guest
	writeProcess(t, env, 103, "4103\n", 4103, "/bin/sleep", "100")          // stale: PID reused by any process
	writeProcess(t, env, 104, "4104\n", 4104)                               // stale: no such process
	writeProcess(t, env, 105, "garbage", 0)

	guests, err := c.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	require.Equal(t, []int{100, 101, 102, 103, 104, 105, 106}, vmids(guests))
	assert.Equal(t, VMStatusRunning, guests[0].Status)
	assert.Equal(t, 4100, guests[0].PID)

	for _, g := range guests[1:] {
		assert.Equal(t, VMStatusStopped, g.Status, "VM %d", g.VMID)
		assert.Zero(t, g.PID, "VM %d", g.VMID)
	}

	guests, err = c.List(context.Background(), ListFilter{Status: VMStatusRunning})
	require.NoError(t, err)
	assert.Equal(t, []int{100}, vmids(guests))

	guests, err = c.List(context.Background(), ListFilter{Status: VMStatusStopped, SkipTemplates: true})
	require.NoError(t, err)
	assert.Equal(t, []int{101, 102, 103, 104, 105}, vmids(guests))
}

func TestClientListOrdersByVMID(t *testing.T) {
	c, _ := newTestClient(t)

	for _, vmid := range []int{1000, 99, 100} {
		writeVMConfig(t, c, vmid, "name: vm\n")
	}

	guests, err := c.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, []int{99, 100, 1000}, vmids(guests))
}

// TestClientListReportsUnreadableConfigs checks that a config that cannot be
// read is reported with the guests that were read, while one that does not
// parse is skipped silently.
func TestClientListReportsUnreadableConfigs(t *testing.T) {
	c, env := newTestClient(t)

	writeVMConfig(t, c, 100, "name: ok\n")
	writeVMConfig(t, c, 101, "cores: many\n")

	// A config path that reads as a directory fails with an *fs.PathError,
	// as an I/O error would, whoever runs the test.
	target := filepath.Join(t.TempDir(), "dir")
	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.Symlink(target, filepath.Join(env.configDir, "qemu-server", "102.conf")))

	guests, err := c.List(context.Background(), ListFilter{})
	assert.Equal(t, []int{100}, vmids(guests))

	var listErr *ListError
	require.ErrorAs(t, err, &listErr)
	assert.Equal(t, []int{102}, slices.Sorted(maps.Keys(listErr.Skipped)))

	var pathErr *fs.PathError
	assert.ErrorAs(t, err, &pathErr, "the cause stays reachable")
	assert.Contains(t, err.Error(), "102")
}
