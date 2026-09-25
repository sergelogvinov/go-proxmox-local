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

package fakelocal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sergelogvinov/go-proxmox-local/lxc"
	"github.com/sergelogvinov/go-proxmox-local/qemu"
)

func TestFakeQuorateAndNextID(t *testing.T) {
	f := New(t, WithQuorum(true), WithNextID(142))
	client := f.Client()

	ready, err := client.Cluster().Quorate(context.Background())
	require.NoError(t, err)
	assert.True(t, ready)

	id, err := client.Cluster().NextID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 142, id)

	id, err = client.Cluster().NextID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 143, id, "NextID increments on each call")
}

func TestFakeQuorumFalse(t *testing.T) {
	f := New(t, WithQuorum(false))
	client := f.Client()

	ready, err := client.Cluster().Quorate(context.Background())
	require.NoError(t, err)
	assert.False(t, ready)
}

func TestFakeCreateThenUpdate(t *testing.T) {
	f := New(t, WithGuest(100, "name: other-vm\ntags: unrelated\n"))
	client := f.Client()

	cores := 64
	cfg := &qemu.Config{
		Name:        "node-capacity",
		Description: "Karpenter discovery service",
		Cores:       &cores,
		Tags:        &qemu.Tags{"karpenter"},
	}

	require.NoError(t, client.Qemu().Create(context.Background(), 101, cfg))
	f.AssertRan("qm", "create", "101", "--cores", "64", "--description", "Karpenter discovery service", "--name", "node-capacity", "--tags", "karpenter")

	got := f.Guest(101)
	assert.Equal(t, "node-capacity", got.Name)
	assert.Equal(t, "Karpenter discovery service", got.Description)
	require.NotNil(t, got.Cores)
	assert.Equal(t, 64, *got.Cores)
	require.NotNil(t, got.Tags)
	assert.Equal(t, qemu.Tags{"karpenter"}, *got.Tags)

	// The other guest WithGuest seeded is untouched.
	other := f.Guest(100)
	assert.Equal(t, "other-vm", other.Name)

	newCores := 96
	require.NoError(t, client.Qemu().Update(context.Background(), 101, &qemu.Config{Name: "node-capacity", Cores: &newCores, Tags: &qemu.Tags{"karpenter"}}))

	updated := f.Guest(101)
	require.NotNil(t, updated.Cores)
	assert.Equal(t, 96, *updated.Cores)
	assert.Equal(t, "Karpenter discovery service", updated.Description, "unrelated fields survive an update")

	require.NoError(t, client.Qemu().Delete(context.Background(), 101))
	f.AssertRan("qm", "destroy", "101")

	_, err := client.Qemu().Get(context.Background(), 101)
	require.Error(t, err, "guest should be gone after Delete")
}

func TestFakeLXCCreateThenUpdate(t *testing.T) {
	f := New(t, WithLXCGuest(200, "hostname: other-ct\n"))
	client := f.Client()

	cores := 2
	cfg := &lxc.Config{Hostname: "web-1", Cores: &cores}

	require.NoError(t, client.LXC().Create(context.Background(), 201, "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst", cfg))
	f.AssertRan("pct", "create", "201", "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst", "--cores", "2", "--hostname", "web-1")

	got := f.LXCGuest(201)
	assert.Equal(t, "web-1", got.Hostname)
	require.NotNil(t, got.Cores)
	assert.Equal(t, 2, *got.Cores)

	// The other container WithLXCGuest seeded is untouched.
	other := f.LXCGuest(200)
	assert.Equal(t, "other-ct", other.Hostname)

	newCores := 4
	require.NoError(t, client.LXC().Update(context.Background(), 201, &lxc.Config{Hostname: "web-1", Cores: &newCores}))

	updated := f.LXCGuest(201)
	require.NotNil(t, updated.Cores)
	assert.Equal(t, 4, *updated.Cores)

	require.NoError(t, client.LXC().Delete(context.Background(), 201))
	f.AssertRan("pct", "destroy", "201")

	_, err := client.LXC().Get(context.Background(), 201)
	require.Error(t, err, "container should be gone after Delete")
}
