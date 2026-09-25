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

// Package fakelocal is the local counterpart to go-proxmox-rest's
// fakeapi: a public test double so a consumer of go-proxmox-local
// (cmd/proxmox-scheduler above all) can drive a full create-then-update
// reconcile against a *local.Client without a hypervisor.
//
// A Fake is a temp root directory tree (etc/pve/qemu-server/*.conf) plus
// a Runner that interprets `qm create`/`qm set`/`qm destroy`,
// `pvecm status` and `pvesh get /cluster/nextid` against it. Writes go
// through the real conf package's reader on the way back out — only the
// write side (what a real `qm` invocation would do to the file) is
// faked — so a config a test's code under test reads back is exactly
// what a Get/List call would decode from a real file.
package fakelocal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	local "github.com/sergelogvinov/go-proxmox-local"
	"github.com/sergelogvinov/go-proxmox-local/conf"
	"github.com/sergelogvinov/go-proxmox-local/qemu"
)

// Fake is a fake Proxmox node. See the package doc comment.
type Fake struct {
	t      testing.TB
	root   string
	quorum bool
	nextID int

	mu  sync.Mutex
	ran [][]string
}

// Option configures a Fake at construction.
type Option func(*Fake)

// WithQuorum sets the result `pvecm status` reports (default true).
func WithQuorum(quorate bool) Option {
	return func(f *Fake) { f.quorum = quorate }
}

// WithNextID sets the first id `pvesh get /cluster/nextid` returns,
// incrementing by one on each subsequent call (default 100).
func WithNextID(id int) Option {
	return func(f *Fake) { f.nextID = id }
}

// WithGuest seeds the fake root with a guest config file, exactly as a
// real /etc/pve/qemu-server/<vmid>.conf would read — content is written
// verbatim, so it can carry a description, a [PENDING] section or
// snapshots, same as any conf-package test fixture.
func WithGuest(vmid int, content string) Option {
	return func(f *Fake) {
		f.t.Helper()

		if err := os.WriteFile(f.guestPath(vmid), []byte(content), 0o600); err != nil {
			f.t.Fatalf("fakelocal: WithGuest(%d): %v", vmid, err)
		}
	}
}

// New creates a Fake rooted at a fresh t.TempDir().
func New(t testing.TB, opts ...Option) *Fake {
	t.Helper()

	f := &Fake{t: t, root: t.TempDir(), quorum: true, nextID: 100}

	if err := os.MkdirAll(filepath.Join(f.root, "etc", "pve", "qemu-server"), 0o755); err != nil {
		t.Fatalf("fakelocal: %v", err)
	}

	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}

	return f
}

// Client returns a *local.Client rooted at the fake tree, backed by this
// Fake as its Runner.
func (f *Fake) Client() *local.Client {
	f.t.Helper()

	c, err := local.New(local.WithRoot(f.root), local.WithRunner(f))
	if err != nil {
		f.t.Fatalf("fakelocal: local.New: %v", err)
	}

	return c
}

// Guest reads back vmid's config through the real read path (Get), for
// asserting on the effect of a create/update the code under test made.
func (f *Fake) Guest(vmid int) *qemu.Config {
	f.t.Helper()

	cfg, err := f.Client().Qemu().Get(context.Background(), vmid)
	if err != nil {
		f.t.Fatalf("fakelocal: Guest(%d): %v", vmid, err)
	}

	return cfg
}

// AssertRan fails the test unless some prior Run call's argv exactly
// equals want (e.g. AssertRan("qm", "create", "101", "--name", "node-capacity")).
func (f *Fake) AssertRan(want ...string) {
	f.t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	for _, got := range f.ran {
		if len(got) != len(want) {
			continue
		}

		match := true

		for i := range got {
			if got[i] != want[i] {
				match = false

				break
			}
		}

		if match {
			return
		}
	}

	f.t.Fatalf("fakelocal: expected a call %v, got %v", want, f.ran)
}

// Ran returns every argv Run has been called with so far, in call order.
func (f *Fake) Ran() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([][]string(nil), f.ran...)
}

// Run implements local.Runner, interpreting pvecm/pvesh/qm against the
// fake tree.
func (f *Fake) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.ran = append(f.ran, append([]string{name}, args...))
	f.mu.Unlock()

	switch name {
	case "pvecm":
		return f.runPvecm(args)
	case "pvesh":
		return f.runPvesh(args)
	case "qm":
		return f.runQM(args)
	default:
		return nil, fmt.Errorf("fakelocal: unknown command %q", name)
	}
}

// guestPath returns the fake tree's on-disk path for vmid's config file.
func (f *Fake) guestPath(vmid int) string {
	return filepath.Join(f.root, "etc", "pve", "qemu-server", strconv.Itoa(vmid)+".conf")
}

func (f *Fake) runPvecm(args []string) ([]byte, error) {
	if len(args) == 0 || args[0] != "status" {
		return nil, fmt.Errorf("fakelocal: pvecm: unsupported args %v", args)
	}

	quorate := "No"
	if f.quorum {
		quorate = "Yes"
	}

	return []byte("Quorate:               " + quorate + "\n"), nil
}

func (f *Fake) runPvesh(args []string) ([]byte, error) {
	if len(args) < 2 || args[0] != "get" || args[1] != "/cluster/nextid" {
		return nil, fmt.Errorf("fakelocal: pvesh: unsupported args %v", args)
	}

	f.mu.Lock()
	id := f.nextID
	f.nextID++
	f.mu.Unlock()

	return []byte(strconv.Itoa(id) + "\n"), nil
}

// runQM interprets `qm create/set/destroy <vmid> [--key value ...]`
// against the fake tree: create/set merge the given flags into the
// guest's existing config (if any) and rewrite the file; destroy removes
// it.
func (f *Fake) runQM(args []string) ([]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("fakelocal: qm: missing action/vmid")
	}

	action, vmidStr := args[0], args[1]

	vmid, err := strconv.Atoi(vmidStr)
	if err != nil {
		return nil, fmt.Errorf("fakelocal: qm: invalid vmid %q", vmidStr)
	}

	path := f.guestPath(vmid)

	switch action {
	case "destroy":
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}

		return nil, nil

	case "create", "set":
		description, fields := f.readGuest(path)

		for key, value := range parseFlags(args[2:]) {
			switch key {
			case "description":
				description = value
			case "digest":
				// qm's optimistic-concurrency argument; the fake does not
				// enforce it — nothing to fake here without a real
				// concurrent writer.
			default:
				fields[key] = value
			}
		}

		if err := os.WriteFile(path, renderGuest(description, fields), 0o600); err != nil {
			return nil, err
		}

		return nil, nil

	default:
		return nil, fmt.Errorf("fakelocal: qm: unsupported action %q", action)
	}
}

// readGuest reads and parses path's existing config, if any, returning
// its description and current-section fields. A missing file returns
// zero values, matching `qm create`'s starting point for a new guest.
func (f *Fake) readGuest(path string) (string, map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", map[string]string{}
	}

	file, err := conf.Parse(bytes.NewReader(data))
	if err != nil {
		f.t.Fatalf("fakelocal: parsing existing guest config: %v", err)
	}

	return file.Description, file.Current.Map()
}

// parseFlags parses a `--key value --key2 value2 ...` argv tail into a
// map, as commandArgs (qemu package) built it.
func parseFlags(args []string) map[string]string {
	out := make(map[string]string, len(args)/2)

	for i := 0; i+1 < len(args); i += 2 {
		key, ok := strings.CutPrefix(args[i], "--")
		if !ok {
			continue
		}

		out[key] = args[i+1]
	}

	return out
}

// renderGuest writes description and fields back out in the PVE
// section-config format conf.Parse expects: description as '#' lines,
// then fields as "key: value" lines in sorted order (the fake has no
// original file order to preserve for a freshly-set key).
func renderGuest(description string, fields map[string]string) []byte {
	var b strings.Builder

	if description != "" {
		for line := range strings.SplitSeq(description, "\n") {
			b.WriteString("#" + line + "\n")
		}
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		b.WriteString(k + ": " + fields[k] + "\n")
	}

	return []byte(b.String())
}
