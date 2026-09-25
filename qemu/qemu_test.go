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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
	restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"
)

// fakeEnv is a minimal Env for tests: ConfigDir is served from a
// directory the test controls, and Run just records the argv it was
// asked to run and replays a scripted error. Being a plain local value
// (not a package-level var, unlike this package's pre-port
// qemuServerDir), these tests are t.Parallel()-safe.
type fakeEnv struct {
	configDir string
	ran       [][]string
	runErr    error
}

func (e *fakeEnv) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	e.ran = append(e.ran, append([]string{name}, args...))

	return nil, e.runErr
}

func (e *fakeEnv) ConfigDir() string { return e.configDir }

func newTestClient(t *testing.T) (*Client, *fakeEnv) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "qemu-server"), 0o755))

	env := &fakeEnv{configDir: dir}

	return New(env), env
}

func writeVMConfig(t *testing.T, c *Client, vmid int, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(c.configPath(vmid), []byte(content), 0o600))
}

func TestFilterChangedOptions(t *testing.T) {
	testCases := []struct {
		name     string
		current  map[string]string
		desired  map[string]string
		expected map[string]string
	}{
		{
			name:     "all unchanged",
			current:  map[string]string{"cores": "4", "name": "test-vm"},
			desired:  map[string]string{"cores": "4"},
			expected: map[string]string{},
		},
		{
			name:     "changed value",
			current:  map[string]string{"cores": "4"},
			desired:  map[string]string{"cores": "8"},
			expected: map[string]string{"cores": "8"},
		},
		{
			name:     "missing from current",
			current:  map[string]string{"cores": "4"},
			desired:  map[string]string{"cores": "4", "tags": "karpenter"},
			expected: map[string]string{"tags": "karpenter"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, filterChangedOptions(tc.current, tc.desired))
		})
	}
}

func TestClientGet(t *testing.T) {
	c, _ := newTestClient(t)

	writeVMConfig(t, c, 100,
		"#Karpenter discovery service\nname: test-vm\ncores: 4\nmemory: 4096\ntags: karpenter;test\naffinity: 0-3\nhostpci0: 0000:81:00.0,pcie=1\n[PENDING]\ncores: 8\n")

	cfg, err := c.Get(context.Background(), 100)
	require.NoError(t, err)

	assert.Equal(t, "test-vm", cfg.Name)
	assert.Equal(t, "Karpenter discovery service", cfg.Description)
	require.NotNil(t, cfg.Cores)
	assert.Equal(t, 4, *cfg.Cores)
	require.NotNil(t, cfg.Memory)
	require.NotNil(t, cfg.Memory.Current)
	assert.Equal(t, 4096, *cfg.Memory.Current)
	require.NotNil(t, cfg.Tags)
	assert.Equal(t, restqemu.Tags{"karpenter", "test"}, *cfg.Tags)
	assert.Equal(t, "0-3", cfg.Affinity)
	require.Contains(t, cfg.HostPCI, 0)
	assert.Equal(t, "0000:81:00.0", cfg.HostPCI[0].Host)
	assert.NotEmpty(t, cfg.Digest, "Get should populate Digest from the file's own hash")

	_, err = c.Get(context.Background(), 999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errdefs.ErrNotFound))
}

func TestClientList(t *testing.T) {
	c, env := newTestClient(t)

	writeVMConfig(t, c, 100, "name: other-vm\ntags: unrelated\n")
	writeVMConfig(t, c, 101, "name: node-capacity\ntags: karpenter\n")
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "qemu-server", "notavm.conf"), []byte("name: broken\n"), 0o600))

	guests, err := c.List(context.Background(), ListFilter{Name: "node-capacity"})
	require.NoError(t, err)
	require.Len(t, guests, 1)
	assert.Equal(t, 101, guests[0].VMID)
	assert.Equal(t, "node-capacity", guests[0].Config.Name)

	guests, err = c.List(context.Background(), ListFilter{Name: "does-not-exist"})
	require.NoError(t, err)
	assert.Empty(t, guests)

	guests, err = c.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	require.Len(t, guests, 2)

	guests, err = c.List(context.Background(), ListFilter{
		Match: func(cfg *Config) (bool, error) { return cfg.Tags != nil && (*cfg.Tags)[0] == "karpenter", nil },
	})
	require.NoError(t, err)
	require.Len(t, guests, 1)
	assert.Equal(t, 101, guests[0].VMID)

	_, err = c.List(context.Background(), ListFilter{
		Match: func(*Config) (bool, error) { return false, assert.AnError },
	})
	require.ErrorIs(t, err, assert.AnError)
}

func TestClientCreate(t *testing.T) {
	c, env := newTestClient(t)

	cfg := &Config{Name: "node-capacity", Digest: "should-never-be-sent"}
	require.NoError(t, c.Create(context.Background(), 101, cfg))
	require.Len(t, env.ran, 1)
	assert.Equal(t, "qm", env.ran[0][0])
	assert.Equal(t, []string{"qm", "create", "101", "--name", "node-capacity"}, env.ran[0])
}

func TestClientDelete(t *testing.T) {
	c, env := newTestClient(t)

	require.NoError(t, c.Delete(context.Background(), 101))
	assert.Equal(t, [][]string{{"qm", "destroy", "101"}}, env.ran)
}

func TestClientUpdate(t *testing.T) {
	c, env := newTestClient(t)

	writeVMConfig(t, c, 100, "name: test-vm\ncores: 4\n")

	require.NoError(t, c.Update(context.Background(), 100, &Config{Name: "test-vm", Cores: new(4)}))
	assert.Empty(t, env.ran, "no qm invocation expected when nothing changed")

	require.NoError(t, c.Update(context.Background(), 100, &Config{Name: "test-vm", Cores: new(8)}))
	require.Len(t, env.ran, 1)
	assert.Equal(t, "qm", env.ran[0][0])
	assert.Equal(t, "set", env.ran[0][1])
	assert.Equal(t, "100", env.ran[0][2])
	assert.Contains(t, env.ran[0], "--cores")
	assert.Contains(t, env.ran[0], "8")
	assert.Contains(t, env.ran[0], "--digest")
}
