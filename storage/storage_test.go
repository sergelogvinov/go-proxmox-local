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

package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeEnv struct {
	output []byte
	err    error
	ran    [][]string
}

func (e *fakeEnv) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	e.ran = append(e.ran, append([]string{name}, args...))

	return e.output, e.err
}

func TestClientStatus(t *testing.T) {
	env := &fakeEnv{output: []byte(`[
		{"storage":"local","type":"dir","total":1000,"used":400,"avail":600,"active":1,"enabled":1},
		{"storage":"local-lvm","type":"lvmthin","total":2000,"used":100,"avail":1900,"active":1,"enabled":1}
	]`)}
	c := New(env)

	statuses, err := c.Status(context.Background())
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "local", statuses[0].Storage)
	assert.Equal(t, int64(1000), statuses[0].TotalSpace)
	assert.Equal(t, [][]string{{"pvesm", "status", "--output-format", "json"}}, env.ran)
}

func TestClientList(t *testing.T) {
	env := &fakeEnv{output: []byte(`[
		{"volid":"local:iso/debian.iso","format":"iso","size":123456}
	]`)}
	c := New(env)

	volumes, err := c.List(context.Background(), "local")
	require.NoError(t, err)
	require.Len(t, volumes, 1)
	assert.Equal(t, "local:iso/debian.iso", volumes[0].VolID)
	assert.Equal(t, [][]string{{"pvesm", "list", "local", "--output-format", "json"}}, env.ran)
}
