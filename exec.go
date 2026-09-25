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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sergelogvinov/go-proxmox-local/internal/errdefs"
)

// Runner executes a PVE CLI tool. The default implementation uses
// exec.CommandContext with an absolute, pre-resolved binary path.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}

// Logger is the minimal logging interface WithDryRun uses to report the
// argv it would have executed. *log.Logger satisfies it.
type Logger interface {
	Printf(format string, args ...any)
}

// pveBinaries are the only commands this module ever shells out to.
// commandRunner resolves each to an absolute path once, at construction.
var pveBinaries = []string{"pvecm", "pvesh", "qm"}

// pveBinaryDirs are searched, in order, for each of pveBinaries — not
// $PATH, since a root daemon should not inherit PATH-based binary
// resolution.
var pveBinaryDirs = []string{"/usr/sbin", "/usr/bin"}

// commandRunner is the default Runner: it resolves pvecm/pvesh/qm to
// absolute paths once, at construction, and shells out via
// exec.CommandContext, capturing stdout and stderr separately.
type commandRunner struct {
	bin     map[string]string // command name -> resolved absolute path, or "" if not found
	timeout time.Duration
	dryRun  bool
	logger  Logger
}

// newCommandRunner resolves pveBinaries against pveBinaryDirs and returns
// a Runner backed by them. Resolution failures are not an error here —
// they surface as ErrNotInstalled from Run, once a caller actually tries
// to invoke the missing binary, since a build/test environment
// legitimately has none of them installed.
func newCommandRunner(timeout time.Duration, dryRun bool, logger Logger) *commandRunner {
	r := &commandRunner{
		bin:     make(map[string]string, len(pveBinaries)),
		timeout: timeout,
		dryRun:  dryRun,
		logger:  logger,
	}

	for _, name := range pveBinaries {
		r.bin[name] = resolveBinary(name)
	}

	return r
}

// resolveBinary searches pveBinaryDirs for name, returning its absolute
// path, or "" if not found in any of them.
func resolveBinary(name string) string {
	for _, dir := range pveBinaryDirs {
		path := filepath.Join(dir, name)

		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}

	return ""
}

// Run implements Runner.
func (r *commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.dryRun {
		if r.logger != nil {
			r.logger.Printf("dry-run: %s %s", name, strings.Join(args, " "))
		}

		return nil, nil
	}

	path, known := r.bin[name]
	if !known {
		return nil, fmt.Errorf("proxmox-local: unknown command %q", name)
	}

	if path == "" {
		return nil, fmt.Errorf("%w: %s", errdefs.ErrNotInstalled, name)
	}

	if _, ok := ctx.Deadline(); !ok && r.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, args...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		cmdErr := &CommandError{
			Cmd:    path,
			Args:   args,
			Stderr: strings.TrimSpace(stderr.String()),
		}

		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			cmdErr.ExitCode = exitErr.ExitCode()
		} else {
			cmdErr.ExitCode = -1
		}

		// PVE's real message is e.g. "can't lock file
		// '/var/lock/qemu-server/lock-100.conf' - got timeout" — surface
		// it as the typed, retryable ErrLocked rather than an opaque
		// CommandError.
		if strings.Contains(cmdErr.Stderr, "can't lock file") {
			return stdout.Bytes(), fmt.Errorf("%w: %w", errdefs.ErrLocked, cmdErr)
		}

		return stdout.Bytes(), cmdErr
	}

	return stdout.Bytes(), nil
}
