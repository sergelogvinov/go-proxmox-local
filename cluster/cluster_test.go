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

package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEnv is a minimal Env for tests: it never runs a real pvecm/pvesh,
// just records the argv it was asked to run and replays a scripted
// output/error. Being a plain local value (not a package-level var),
// these tests are t.Parallel()-safe.
type fakeEnv struct {
	output []byte
	err    error
	ran    [][]string
}

func (e *fakeEnv) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	e.ran = append(e.ran, append([]string{name}, args...))

	return e.output, e.err
}

func TestParseQuorate(t *testing.T) {
	testCases := []struct {
		name      string
		output    string
		expected  bool
		expectErr bool
	}{
		{
			name: "quorate yes",
			output: "Cluster information\n" +
				"-------------------\n" +
				"Name:             mycluster\n\n" +
				"Quorum information\n" +
				"------------------\n" +
				"Quorate:               Yes\n",
			expected: true,
		},
		{
			name:     "quorate numeric one",
			output:   "Quorate:                  1\n",
			expected: true,
		},
		{
			name:     "not quorate",
			output:   "Quorate:               No\n",
			expected: false,
		},
		{
			name:     "not quorate numeric zero",
			output:   "Quorate:               0\n",
			expected: false,
		},
		{
			name:      "no quorate line",
			output:    "Cluster information\n-------------------\nName:             mycluster\n",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ready, err := parseQuorate(tc.output)

			if tc.expectErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, ready)
		})
	}
}

func TestClientQuorate(t *testing.T) {
	env := &fakeEnv{output: []byte("Quorate:               Yes\n")}
	c := New(env)

	ready, err := c.Quorate(context.Background())
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, [][]string{{"pvecm", "status"}}, env.ran)
}

func TestClientNextID(t *testing.T) {
	env := &fakeEnv{output: []byte("142\n")}
	c := New(env)

	id, err := c.NextID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 142, id)
	assert.Equal(t, [][]string{{"pvesh", "get", "/cluster/nextid"}}, env.ran)
}
