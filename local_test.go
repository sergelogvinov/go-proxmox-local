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
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaults(t *testing.T) {
	c, err := New()
	require.NoError(t, err)
	assert.Equal(t, "/etc/pve", c.ConfigDir())
	assert.Equal(t, "/run/qemu-server", c.RunDir())
}

func TestNewWithRoot(t *testing.T) {
	c, err := New(WithRoot("/srv/fake-root"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/srv/fake-root", "etc/pve"), c.ConfigDir())
	assert.Equal(t, filepath.Join("/srv/fake-root", "run/qemu-server"), c.RunDir())
}

func TestNewWithRootAndExplicitDirs(t *testing.T) {
	// Order must not matter: an explicit WithConfigDir/WithRunDir always
	// wins over the root-derived default, regardless of where it appears
	// relative to WithRoot in the option list.
	before, err := New(WithConfigDir("/custom/pve"), WithRoot("/srv/fake-root"))
	require.NoError(t, err)
	assert.Equal(t, "/custom/pve", before.ConfigDir())

	after, err := New(WithRoot("/srv/fake-root"), WithConfigDir("/custom/pve"))
	require.NoError(t, err)
	assert.Equal(t, "/custom/pve", after.ConfigDir())
	assert.Equal(t, filepath.Join("/srv/fake-root", "run/qemu-server"), after.RunDir())
}

func TestRunNotInstalled(t *testing.T) {
	c, err := New()
	require.NoError(t, err)

	_, err = c.Run(context.Background(), "pvecm", "status")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotInstalled)
}

type fakeLogger struct {
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func TestRunDryRun(t *testing.T) {
	logger := &fakeLogger{}
	c, err := New(WithDryRun(true), WithLogger(logger))
	require.NoError(t, err)

	out, err := c.Run(context.Background(), "qm", "destroy", "100")
	require.NoError(t, err)
	assert.Nil(t, out)
	require.Len(t, logger.lines, 1)
	assert.Contains(t, logger.lines[0], "qm destroy 100")
}

func TestRunWithCustomRunner(t *testing.T) {
	runner := &stubRunner{output: []byte("ok")}
	c, err := New(WithRunner(runner))
	require.NoError(t, err)

	out, err := c.Run(context.Background(), "pvecm", "status")
	require.NoError(t, err)
	assert.Equal(t, []byte("ok"), out)
	assert.Equal(t, []string{"pvecm"}, runner.calledWith)
}

type stubRunner struct {
	output     []byte
	calledWith []string
}

func (r *stubRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	r.calledWith = append(r.calledWith, name)

	return r.output, nil
}
