# go-proxmox-local — Design Document

> A Go library for driving a Proxmox VE hypervisor **from the node itself** — reading
> `/etc/pve` directly and shelling out to `qm` / `pct` / `pvesm` / `pvesh` / `pvecm` — as a
> sibling to [`go-proxmox-rest`](https://github.com/sergelogvinov/go-proxmox-rest) (the REST
> client) and `go-proxmox-pool` (cluster-wide scheduling helpers).

This document describes the module `github.com/sergelogvinov/go-proxmox-local`: what it
owns, how it is structured, how it is tested without a hypervisor, and how
`karpenter-provider-proxmox` migrated onto it. The migration is done; what follows is a
description of the module as built, plus the work that remains open.

## Goals & Non-Goals

### Goals

- **One place for "Proxmox, locally".** Every read of `/etc/pve` and every
  `qm`/`pct`/`pvesm`/`pvesh`/`pvecm` invocation lives behind a typed API, instead of being
  scattered across provider packages. Done.
- **A real parser for the PVE section-config format.** `#` description comments,
  `key: value` lines, `[PENDING]` and `[<snapshot>]` sections — see the `conf` package
  (§6.1). Done.
- **One definition of a guest, shared with `go-proxmox-rest`.** The guest config struct
  is not redefined here — `qemu.Config`/`lxc.Config` are type aliases to
  `go-proxmox-rest`'s types. This module owns the *file format*; the REST module owns the
  *types*. See §6. Done, though the property-string parser itself ended up duplicated
  rather than shared — see §6.3/§6.4.
- **Read direct, write through `qm`/`pct`.** Reads go straight to the filesystem. Writes
  never touch `/etc/pve`: they go through `qm`/`pct` create/set/destroy, which own
  locking, schema validation and pmxcfs replication (§7). Done.
- **Testable without a hypervisor.** Two seams — a filesystem root and a command runner —
  let the whole surface be exercised against a temp directory and a scripted `Runner`
  (`fakelocal`, the local counterpart to `go-proxmox-rest`'s `fakeapi`). Done.
- **Idiomatic and consistent with the sibling module.** Same fluent shape
  (`client.Qemu().Get(ctx, vmid)`, mirroring `Get`/`List`/`Create`/`Update`/`Delete` as used
  by `go-proxmox-rest`'s `pools`/`storage`/`nodes/network` packages), same `Option`
  pattern, same typed errors and predicates. Done.
- **Reusable beyond Karpenter.** A `qm`/`pct`-wrapping library with a config parser is
  useful to any on-node agent (backup hooks, monitoring, migration tooling). The `lxc` and
  `storage` packages exist for this reason, not because `karpenter-provider-proxmox` needs
  them yet.

### Non-Goals

- Replacing `go-proxmox-rest`. Anything that needs the cluster's view, another node's
  guests, or authentication belongs there. This module never opens a socket.
- Writing `/etc/pve` files directly. All mutations go through `qm`/`pct`, which own
  locking, validation and pmxcfs replication (§7).
- Full coverage of every `qm`/`pct`/`pvesh`/`pvesm` subcommand. The design makes adding
  one cheap; that doesn't mean every one is added up front.

## Module layout

```
go-proxmox-local
├── go.mod                  // module github.com/sergelogvinov/go-proxmox-local
│                           //   require github.com/sergelogvinov/go-proxmox-rest
├── local.go                // Client, New(), Qemu()/Cluster()/LXC()/Storage()
├── options.go              // WithRoot, WithConfigDir, WithRunDir, WithRunner,
│                           //   WithLogger, WithTimeout, WithDryRun
├── exec.go                 // Runner interface + default exec.CommandContext runner
├── errors.go               // re-exported sentinels + CommandError/ConfigError + predicates
├── doc.go
├── conf/                   // the PVE section-config FILE format — nothing about values
│   ├── conf.go             // File, Section, Parse, Format
│   ├── digest.go           // File.Digest(), for optimistic concurrency
│   └── testdata/           // round-trip corpus of real configs
├── cluster/
│   └── cluster.go          // Quorate (pvecm), NextID (pvesh) — still forks; see §12
├── qemu/
│   ├── qemu.go             // Client: Get, List, ConfigFile, Create, Update, Delete
│   ├── config.go           // DecodeConfig/EncodeConfig — this module's own codec (§6.4)
│   ├── lifecycle.go        // Start, Stop, Reboot (qm)
│   ├── process.go          // ParseVfioPCIDevices(cmdlineArgs) — no /run/qemu-server
│   │                       //   pid scanning yet (§12)
│   └── types.go            // Config, CPU, HostPCI, Memory, NUMA, Tags — aliases
├── lxc/                    // mirrors qemu/'s shape, for containers
│   ├── lxc.go              // Client: Get, List, ConfigFile, Create, Update, Delete (pct)
│   ├── config.go           // DecodeConfig/EncodeConfig, this module's own codec
│   └── types.go            // Config, Features, MountPoint, Net, RootFS — aliases
├── storage/
│   └── storage.go          // Client: Status (pvesm status), List (pvesm list)
├── fakelocal/              // public test double: temp root + scripted Runner
│   └── fakelocal.go        // interprets qm/pct create|set|destroy, pvecm status,
│                           //   pvesh get /cluster/nextid
├── internal/
│   ├── errdefs/            // sentinel errors, shared by root+subpackages without a cycle
│   ├── params/             // ported from go-proxmox-rest/internal/params (§6.4)
│   └── property/           // copied from go-proxmox-rest/internal/property (§6.4)
└── docs/design.md          // this document
```

Package naming follows the sibling module: the root package is `local`.

### Dependency direction

```
local (root)  ->  cluster, qemu, lxc, storage, stdlib
    ^
    |
qemu / lxc  ->  internal/errdefs, internal/params, conf, go-proxmox-rest/nodes/{qemu,lxc}
storage     ->  internal/params, go-proxmox-rest/nodes/storage
cluster     ->  stdlib only
    ^
    |
conf  ->  stdlib          // leaf: no dep on root, on a sibling, or on go-proxmox-rest
```

Same acyclic rule as `go-proxmox-rest`: a subpackage never imports another subpackage, and
never imports root — root imports them. Each subpackage defines its own `Env` interface
(the subset of `*local.Client` it needs — `Run`, and `ConfigDir` where relevant); this is
what lets `*local.Client` satisfy every subpackage's `Env` without those subpackages
importing the `local` package (which imports them, and would cycle):

```go
func (c *Client) Qemu() *qemu.Client       { return qemu.New(c) }
func (c *Client) Cluster() *cluster.Client { return cluster.New(c) }
func (c *Client) LXC() *lxc.Client         { return lxc.New(c) }
func (c *Client) Storage() *storage.Client { return storage.New(c) }
```

There is no shared "env" struct type — each subpackage's `Env` interface is declared
independently (structurally identical between `qemu.Env` and `lxc.Env`, but distinct Go
types, since Go interfaces can't be shared across packages without one importing the
other).

---

## The two seams

Everything this library does is one of two things: **read a file under a known root**, or
**run a PVE binary**. Both get an interface, which is what makes the module testable.

```go
// Runner executes a PVE CLI tool. The default implementation uses
// exec.CommandContext with an absolute, pre-resolved binary path.
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}
```

```go
// Paths are derived from a single root so a test can point the whole library
// at t.TempDir(). Individual directories can still be overridden, in either
// option order — an explicit WithConfigDir/WithRunDir always wins.
WithRoot("/")                          // default
WithConfigDir("/etc/pve")              // pmxcfs mount
WithRunDir("/run/qemu-server")         // pid/vnc/qmp sockets
```

The default `Runner` (`commandRunner` in `exec.go`):

- Resolves `pct`, `pvecm`, `pvesh`, `pvesm`, `qm` to absolute paths once, at `New()`
  (searching `/usr/sbin` then `/usr/bin`), not via `$PATH` per call. Resolution failure is
  not itself an error — it surfaces as `ErrNotInstalled` only when a caller actually
  invokes that binary, since a build/test environment legitimately has none of them.
- `WithTimeout` supplies a fallback deadline when the caller's `ctx` carries none.
- stdout and stderr are captured separately.
- `WithDryRun(true)` (via `WithLogger`) logs the argv and returns success without
  executing, checked *before* binary resolution — dry-run works even when no PVE tooling
  is installed at all.
- A `CommandError`'s stderr containing `"can't lock file"` is additionally wrapped as
  `ErrLocked`.

`fakelocal` supplies a `Runner` that records invocations and applies the effect of
`qm`/`pct` `create`/`set`/`destroy` to the fake root's config tree (§8).

---

## Value parsing and types: reuse, don't reinvent (mostly)

The boundary between the two modules:

> **`go-proxmox-local` owns "config file ⇄ key/value map".
> `go-proxmox-rest` owns "key/value map ⇄ Go struct".**

A guest is a guest whether you learned about it from `pvedaemon` over HTTPS or from
`/etc/pve/qemu-server/100.conf` on the local disk. Only the *transport* differs.

### 6.1 The `conf` package — file format only

```go
type File struct {
    Description string             // '#' lines joined with '\n', comment markers stripped
    Current     Section            // the active configuration
    Pending     Section            // [PENDING]; zero value (no keys) means absent
    Snapshots   map[string]Section // keyed by snapshot name

    raw []byte // exact bytes Parse read, when parsed — see Digest
}

type Section struct { /* keys []string; values map[string]string */ }

func (s Section) Map() map[string]string     // the handoff to DecodeConfig

func Parse(r io.Reader) (*File, error)
func (f *File) Format(w io.Writer) error
func (f *File) Digest() string               // sha1 of the raw text, PVE's concurrency token
```

- **Values are `string`, always.** `conf` never interprets one (fixes 2.3).
- **`Description` is a first-class field**, reconstructed from the leading `#` lines
  (fixes 2.1).
- **Every section is parsed**, not truncated: `Current`, `Pending`, every `[<snapshot>]`
  (fixes 2.2).
- **Round-trip fidelity within a section.** Key order and unknown keys survive
  `Format(Parse(x))`. Section *order* across multiple named snapshots does not survive —
  `Snapshots` is a `map[string]Section`, so `Format` writes them back in sorted name
  order rather than original file order. `Digest` is unaffected: for a parsed `File` it
  hashes the raw bytes `Parse` read, not `Format`'s output.

### 6.2 The typed layer — `go-proxmox-rest/nodes/{qemu,lxc}.Config`

`go-proxmox-rest` already models a guest completely. `qemu.Config`/`lxc.Config` in this
module are type aliases:

```go
// qemu/types.go
package qemu

import restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"

type Config  = restqemu.Config
type CPU     = restqemu.CPU
type HostPCI = restqemu.HostPCI
type Memory  = restqemu.Memory
type NUMA    = restqemu.NUMA
type Tags    = restqemu.Tags
```

(`lxc/types.go` aliases `Config`, `Features`, `MountPoint`, `Net`, `RootFS` the same way.)

A `*qemu.Config` obtained from the local filesystem is *the identical Go type* as one
fetched over REST — `karpenter-provider-proxmox`'s `cmd/proxmox-scheduler` (local) and
`pkg/providers/…` (REST) share one notion of what a guest config is.

### Property strings — duplicated, not shared

`go-proxmox-rest/internal/property` converts Proxmox comma-separated property strings to
and from tagged structs (`cfg:"name[,default]"` tags). The original plan (§6.4 below) was
to reuse this as-is by promoting it to a public package upstream. That didn't happen, so
`go-proxmox-local/internal/property` is a **copy** of it, and
`go-proxmox-local/internal/params` is a **port** of `go-proxmox-rest/internal/params` (the
reflection-based `url`-tag encoder/decoder `DecodeConfig`/`EncodeConfig` need for the
*ordinary* named fields — the property-string handling for individual indexed entries
comes for free from each `restqemu`/`restlxc` type's own exported `UnmarshalJSON`/
`String()` methods instead. As a result `internal/property` currently has **no callers
outside its own tests** in this module — it was copied in ahead of a codec that ended up
not needing it directly. Kept rather than deleted on the assumption a future `qm`/`pct`
subcommand wrapper will need to build or parse a property string directly, the way
`go-proxmox-rest`'s own resource packages do.

### The cost: pointer fields

`qemu.Config`/`lxc.Config` use pointers (`*int`, `*bool`, `*Memory`) so writes can
distinguish "unchanged" from "explicitly zero" — correct for the REST module's purposes.
Read-mostly local callers pay for it:

```go
// before
if vmConfig.Cores == cpus.Size() { … }

// after
if lo.FromPtr(cfg.Cores) == cpus.Size() { … }
```

`Memory` is the sharper edge: `*Memory` with a `Current *int` field, so
`cfg.Memory.Current` is a two-level dereference — `memoryMB` in
`cmd/proxmox-scheduler/vm.go` wraps this. `karpenter-provider-proxmox` already depends on
`samber/lo`, so `lo.FromPtr` is idiomatic at those call sites.

---

## The read/write asymmetry

**Reads go direct. Writes go through `qm`/`pct`.** This is the module's central operating
rule.

### Reads: straight to the filesystem

`Get`, `List`, `ConfigFile` (both `qemu` and `lxc`) all read files directly — no fork, no
lock, no `pvedaemon` round-trip. `Quorate`/`NextID` (`cluster`) are the exception: they
still fork `pvecm status`/`pvesh get /cluster/nextid` rather than reading
`/etc/pve/.members`/`.vmlist` — the local-first read this section originally promised is
open work (§12).

### Writes: never touch `/etc/pve`

pmxcfs holds a per-file write lock and replicates every commit to the cluster; `qm`/`pct`
additionally take a per-guest flock and validate values against the PVE schema before
writing. `conf.Format` exists for round-trip tests and digest computation — not as a
write path.

- `Update` → `qm`/`pct set <vmid> --key value …`, `Create` → `qm`/`pct create`, `Delete` →
  `qm`/`pct destroy`.
- The arguments come from `EncodeConfig(cfg)` — the same shape `qm set`/`pct set` want.
- A `CommandError` whose stderr contains `"can't lock file"` is wrapped as `ErrLocked`
  (retryable) by the default `Runner` — see §5.
- `WithDryRun(true)` logs the argv and skips execution.

### Correct change detection

```go
func (c *Client) Update(ctx context.Context, vmid int, cfg *Config) error
```

1. Read and parse the current config (`ConfigFile` → `conf.Parse`).
2. Decode it (`DecodeConfig`), set its `Description` from `file.Description` (§6.1),
   then re-encode it (`EncodeConfig`) — and separately encode the caller's desired `cfg`,
   with `Digest` always cleared first regardless of what the caller passed (see
   `encodeForWrite` in both `qemu.go`/`lxc.go`).
3. Diff **string against string** (`filterChangedOptions`), never `any == any` — fixes
   2.4 by construction, since both sides went through the identical encoder.
4. If nothing differs, return without invoking `qm`/`pct`. Otherwise add a fresh
   `--digest` (computed from the file just read, not from anything the caller supplied —
   see §7.4) and run `qm`/`pct set` with only the differing keys.

### Optimistic concurrency

`Update` always sends `--digest <sha1 of the config just read>` when it has any changes
to write, closing the read-modify-write race against a concurrent `qm`/`pct set` or a
web-UI edit. There is no `WithDigestChecks(false)` escape hatch (open item, §12); a PVE
version where `--digest` doesn't behave as expected currently has no way to disable the
check short of patching the caller's `cfg.Digest` handling.

---

## Testing

`fakelocal` is the local counterpart to `go-proxmox-rest`'s `fakeapi`:

```go
func TestSchedulerCreatesCapacityVM(t *testing.T) {
    f := fakelocal.New(t,
        fakelocal.WithQuorum(true),
        fakelocal.WithGuest(100, "name: other-vm\ntags: unrelated\n"),
    )
    client := f.Client()   // a *local.Client rooted at the fake tree

    // … drive the code under test …

    f.AssertRan("qm", "create", "101", "--name", "node-capacity")
    cfg := f.Guest(101)    // *qemu.Config, decoded from the fake tree
    require.Equal(t, qemu.Tags{"karpenter"}, *cfg.Tags)
}
```

What `fakelocal` provides today:

- A temp root with `etc/pve/qemu-server/*.conf` and `etc/pve/lxc/*.conf`.
- A `Runner` that records every argv (`Ran()`/`AssertRan`) and interprets
  `qm`/`pct create|set|destroy`, `pvesh get /cluster/nextid` and `pvecm status` against
  that tree — writes go through the same merge-then-render logic (not through
  `conf.Format`), reads go through the real `Get`, so what a test reads back is exactly
  what a real file would decode to.
- `WithGuest`/`WithLXCGuest` to seed a guest's initial config file verbatim (so it can
  carry a description, `[PENDING]`, snapshots — anything `conf`'s own testdata corpus
  covers).

What it does *not* provide (open items, §12): `etc/pve/.members`/`.vmlist`,
`run/qemu-server/*.pid`, and fault injection (a scripted command failure/lock
error/hang) — none of these have a caller yet, since `cluster` doesn't read `.members`/
`.vmlist` and no process-discovery method exists to fail.

`conf` has its own table-driven round-trip tests over a corpus of real configs in
`testdata/` (a plain config, a multi-line description, `[PENDING]`, one snapshot,
multiple snapshots, and values that would break a YAML parser). `qemu`/`lxc` each have a
`DecodeConfig(EncodeConfig(cfg)) == cfg` round-trip test plus a cross-transport test
(decoding the same guest from a JSON-shaped map vs. a config-file-shaped map and
asserting equal `*Config` values) — the contract this module's own codec has to hold,
now that it isn't shared with `go-proxmox-rest` (§6.4).

---

## Error handling

```go
var (
    ErrNotFound     = errors.New("guest not found")       // no config file for that vmid
    ErrNoQuorum     = errors.New("cluster has no quorum")
    ErrNotInstalled = errors.New("proxmox tooling not found")  // qm/pct/pvesh/pvesm/pvecm missing
    ErrLocked       = errors.New("guest config is locked")     // retryable
)

// CommandError carries what a PVE CLI invocation actually did.
type CommandError struct {
    Cmd      string
    Args     []string
    ExitCode int
    Stderr   string
}

// ConfigError carries the file and line a parse failed on. Declared but not yet
// constructed anywhere — conf.Parse does not currently attribute errors to a line.
type ConfigError struct { Path string; Line int; Err error }

func IsNotFound(err error) bool
func IsNoQuorum(err error) bool
func IsLocked(err error) bool     // callers should retry with backoff
```

The sentinel values live in `internal/errdefs` and are re-exported as the same values
(not copies) from the root `local` package; `qemu`/`lxc` import `internal/errdefs`
directly to wrap `ErrNotFound` themselves — this is what lets them return/wrap it
without importing `local` (which would cycle back to them). `cluster` and `storage`
don't currently return any of these sentinels — `cluster`'s two methods have no
not-found case, and `storage` just wraps whatever `pvesm` failure it gets.

`ErrNotInstalled` matters for the consumer's test and dev environments: the karpenter
controller binary and the e2e suite do not run on a hypervisor, and "the tool is absent"
is distinguishable from "the command failed".

Errors wrap with `%w` throughout.

---

## API sketch

```go
client, err := local.New(
    local.WithLogger(logger),
    local.WithTimeout(30*time.Second),
)
if err != nil { … }

ok, err := client.Cluster().Quorate(ctx)
id,  err := client.Cluster().NextID(ctx)

// qemu — config (direct filesystem reads)
cfg,  err := client.Qemu().Get(ctx, 100)          // *qemu.Config = *restqemu.Config
file, err := client.Qemu().ConfigFile(ctx, 100)   // *conf.File: sections, order, digest
guests, err := client.Qemu().List(ctx, qemu.ListFilter{
    Name: "node-capacity",
    Match: func(c *qemu.Config) (bool, error) {
        return c.Tags != nil && slices.Contains(*c.Tags, "karpenter"), nil
    },
})                                                 // []qemu.Guest{VMID, Config}; empty, not
                                                    //   an error, when nothing matches

// qemu — lifecycle (always via qm)
err = client.Qemu().Create(ctx, vmid, cfg)         // qm create
err = client.Qemu().Update(ctx, vmid, cfg)         // qm set, diffed + digest-guarded
err = client.Qemu().Delete(ctx, vmid)              // qm destroy
err = client.Qemu().Start(ctx, vmid)               // qm start
err = client.Qemu().Stop(ctx, vmid)                // qm stop
err = client.Qemu().Reboot(ctx, vmid)              // qm reboot

devs := qemu.ParseVfioPCIDevices(cmdlineArgs)

// lxc — mirrors qemu exactly, via pct
cfg, err := client.LXC().Get(ctx, 200)
err = client.LXC().Create(ctx, vmid, "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst", cfg)
err = client.LXC().Update(ctx, vmid, cfg)
err = client.LXC().Delete(ctx, vmid)

// storage — pvesm, the one package that always forks (no local file to read instead)
statuses, err := client.Storage().Status(ctx)          // pvesm status
volumes,  err := client.Storage().List(ctx, "local")   // pvesm list local
```

`List` takes a `ListFilter` struct (a typed field or two plus a `Match func(*Config)
(bool, error)` escape hatch, evaluated last) rather than a predicate function or a
variadic OR of filters — mirroring `go-proxmox-rest`'s `cluster.ListFilter`, and removing
2.9's surprises: `Match`'s error aborts and propagates; nothing matching is an empty
slice, not `ErrNotFound`. `Get` (not `Config`) matches `go-proxmox-rest`'s `Get(ctx, id)`
convention used by `pools`/`storage`/`nodes/network`/`nodes/replication` — a deliberate
naming choice over `nodes/qemu.Config`'s own method name, made when wiring this module
into `karpenter-provider-proxmox`.

`Create`/`Update` take a `*Config` rather than a `map[string]any`: the same struct reads
and writes, encoded by `EncodeConfig`. `ConfigFile` is the escape hatch for callers that
need the raw sections, key order or an unmodelled key.
