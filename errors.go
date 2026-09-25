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
	"errors"
	"fmt"
	"strings"

	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
)

// These are re-exports (the same values, not copies) of the sentinels
// internal/errdefs defines, so that an error returned by the cluster or
// qemu subpackages — which wrap errdefs' values directly, to avoid
// importing this package and cycling back — still satisfies
// errors.Is(err, local.ErrNotFound) etc. here.
var (
	// ErrNotFound is returned when no guest config exists for the
	// requested id.
	ErrNotFound = errdefs.ErrNotFound
	// ErrNoQuorum is returned when the local cluster has no quorum.
	ErrNoQuorum = errdefs.ErrNoQuorum
	// ErrNotInstalled is returned when pvecm/pvesh/qm cannot be found on
	// this node.
	ErrNotInstalled = errdefs.ErrNotInstalled
	// ErrLocked is returned when qm reports the guest config is locked by
	// another operation; retryable.
	ErrLocked = errdefs.ErrLocked
)

// CommandError carries what a PVE CLI invocation actually did, for a
// command that exited non-zero.
type CommandError struct {
	Cmd      string
	Args     []string
	ExitCode int
	Stderr   string
}

// Error implements the error interface.
func (e *CommandError) Error() string {
	return fmt.Sprintf("%s %s: exit %d: %s", e.Cmd, strings.Join(e.Args, " "), e.ExitCode, e.Stderr)
}

// ConfigError carries the file and line a config parse failed on.
type ConfigError struct {
	Path string
	Line int
	Err  error
}

// Error implements the error interface.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s:%d: %v", e.Path, e.Line, e.Err)
}

// Unwrap allows errors.Is/errors.As to see through a ConfigError to its
// underlying cause.
func (e *ConfigError) Unwrap() error {
	return e.Err
}

// IsNotFound reports whether err indicates that the requested guest was
// not found.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsNoQuorum reports whether err indicates the local cluster has no
// quorum.
func IsNoQuorum(err error) bool {
	return errors.Is(err, ErrNoQuorum)
}

// IsLocked reports whether err indicates a guest's config is locked by
// another operation; callers should retry with backoff.
func IsLocked(err error) bool {
	return errors.Is(err, ErrLocked)
}
