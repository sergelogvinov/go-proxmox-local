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

import "time"

// config accumulates every Option passed to New.
type config struct {
	root      string
	configDir string
	runDir    string
	runner    Runner
	logger    Logger
	timeout   time.Duration
	dryRun    bool
}

// Option mutates a Client's construction-time configuration.
type Option func(*config)

// WithRoot points every default path (config dir, run dir) at root
// instead of "/" — e.g. WithRoot(t.TempDir()) in a test. It can be
// combined, in either order, with WithConfigDir/WithRunDir: an explicit
// directory always wins over the root-derived default.
func WithRoot(root string) Option {
	return func(c *config) { c.root = root }
}

// WithConfigDir overrides the pmxcfs mount root (default:
// "<root>/etc/pve").
func WithConfigDir(dir string) Option {
	return func(c *config) { c.configDir = dir }
}

// WithRunDir overrides the directory holding running guests'
// pid/vnc/qmp sockets (default: "<root>/run/qemu-server").
func WithRunDir(dir string) Option {
	return func(c *config) { c.runDir = dir }
}

// WithRunner overrides the default exec.CommandContext-based Runner —
// the seam a test's own double drives this library through, without a
// hypervisor.
func WithRunner(r Runner) Option {
	return func(c *config) { c.runner = r }
}

// WithLogger sets the logger WithDryRun uses to report the argv it would
// have executed.
func WithLogger(l Logger) Option {
	return func(c *config) { c.logger = l }
}

// WithTimeout sets the default deadline applied to a call whose context
// carries none. Unset (the default), calls have no enforced timeout
// beyond whatever the caller's own context already carries.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithDryRun makes the default Runner log the argv it would have
// executed (via WithLogger) and return success without running anything,
// when dryRun is true — a root daemon's safety valve.
func WithDryRun(dryRun bool) Option {
	return func(c *config) { c.dryRun = dryRun }
}
