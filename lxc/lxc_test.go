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

package lxc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
)

// fakeEnv is a minimal Env for tests, mirroring qemu package's own.
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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lxc"), 0o755))

	env := &fakeEnv{configDir: dir}

	return New(env), env
}

func writeCTConfig(t *testing.T, c *Client, vmid int, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(c.configPath(vmid), []byte(content), 0o600))
}

func TestFilterChangedOptions(t *testing.T) {
	current := map[string]string{"cores": "4"}
	desired := map[string]string{"cores": "4", "hostname": "web-1"}

	assert.Equal(t, map[string]string{"hostname": "web-1"}, filterChangedOptions(current, desired))
}

func TestClientGet(t *testing.T) {
	c, _ := newTestClient(t)

	writeCTConfig(t, c, 200,
		"#managed by karpenter\nhostname: web-1\ncores: 2\nmemory: 512\nnet0: name=eth0,bridge=vmbr0,ip=dhcp\n")

	cfg, err := c.Get(context.Background(), 200)
	require.NoError(t, err)

	assert.Equal(t, "web-1", cfg.Hostname)
	assert.Equal(t, "managed by karpenter", cfg.Description)
	require.NotNil(t, cfg.Cores)
	assert.Equal(t, 2, *cfg.Cores)
	require.NotNil(t, cfg.Memory)
	assert.Equal(t, 512, *cfg.Memory)
	require.Contains(t, cfg.Net, 0)
	assert.Equal(t, "eth0", cfg.Net[0].Name)
	assert.NotEmpty(t, cfg.Digest)

	_, err = c.Get(context.Background(), 999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errdefs.ErrNotFound))
}

func TestClientList(t *testing.T) {
	c, env := newTestClient(t)

	writeCTConfig(t, c, 200, "hostname: other-ct\n")
	writeCTConfig(t, c, 201, "hostname: web-1\n")
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "lxc", "notact.conf"), []byte("hostname: broken\n"), 0o600))

	guests, err := c.List(context.Background(), ListFilter{Hostname: "web-1"})
	require.NoError(t, err)
	require.Len(t, guests, 1)
	assert.Equal(t, 201, guests[0].VMID)

	guests, err = c.List(context.Background(), ListFilter{Hostname: "does-not-exist"})
	require.NoError(t, err)
	assert.Empty(t, guests)

	guests, err = c.List(context.Background(), ListFilter{})
	require.NoError(t, err)
	assert.Len(t, guests, 2)
}

func TestClientCreate(t *testing.T) {
	c, env := newTestClient(t)

	cfg := &Config{Hostname: "web-1", Digest: "should-never-be-sent"}
	require.NoError(t, c.Create(context.Background(), 201, "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst", cfg))

	require.Len(t, env.ran, 1)
	assert.Equal(t, []string{
		"pct", "create", "201", "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst",
		"--hostname", "web-1",
	}, env.ran[0])
}

func TestClientDelete(t *testing.T) {
	c, env := newTestClient(t)

	require.NoError(t, c.Delete(context.Background(), 201))
	assert.Equal(t, [][]string{{"pct", "destroy", "201"}}, env.ran)
}

func TestClientUpdate(t *testing.T) {
	c, env := newTestClient(t)

	writeCTConfig(t, c, 200, "hostname: web-1\ncores: 2\n")

	cores2 := 2
	require.NoError(t, c.Update(context.Background(), 200, &Config{Hostname: "web-1", Cores: &cores2}))
	assert.Empty(t, env.ran, "no pct invocation expected when nothing changed")

	cores4 := 4
	require.NoError(t, c.Update(context.Background(), 200, &Config{Hostname: "web-1", Cores: &cores4}))
	require.Len(t, env.ran, 1)
	assert.Equal(t, "pct", env.ran[0][0])
	assert.Equal(t, "set", env.ran[0][1])
	assert.Equal(t, "200", env.ran[0][2])
	assert.Contains(t, env.ran[0], "--cores")
	assert.Contains(t, env.ran[0], "4")
	assert.Contains(t, env.ran[0], "--digest")
}
