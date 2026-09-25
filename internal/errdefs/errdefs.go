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

// Package errdefs holds the sentinel errors shared by the local package
// and its cluster/qemu subpackages. It exists so those subpackages can
// return (and wrap) the same error values the root package exposes as
// local.ErrNotFound etc., without importing the root package — which
// would cycle back, since the root package imports them to wire up
// Client.Qemu()/Client.Cluster().
package errdefs

import "errors"

var (
	// ErrNotFound is returned when no guest config exists for the
	// requested id.
	ErrNotFound = errors.New("guest not found")
	// ErrNoQuorum is returned when the local cluster has no quorum.
	ErrNoQuorum = errors.New("cluster has no quorum")
	// ErrNotInstalled is returned when pvecm/pvesh/qm cannot be found on
	// this node.
	ErrNotInstalled = errors.New("proxmox tooling not found")
	// ErrLocked is returned when qm reports the guest config is locked by
	// another operation; retryable.
	ErrLocked = errors.New("guest config is locked")
)
