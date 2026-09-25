# go-proxmox-local — Design Document

> A Go library for driving a Proxmox VE hypervisor **from the node itself** — reading
> `/etc/pve` directly and shelling out to `qm` / `pvesh` / `pvecm` — as a sibling to
> [`go-proxmox-rest`](https://github.com/sergelogvinov/go-proxmox-rest) (the REST client)
> and `go-proxmox-pool` (cluster-wide scheduling helpers).

This document describes the proposed module `github.com/sergelogvinov/go-proxmox-local`:
what it owns, how it is structured, how it is tested without a hypervisor, and how
`karpenter-provider-proxmox` migrates onto it.

The primary consumer is `cmd/proxmox-scheduler` — a daemon that runs **on** each
Proxmox node (packaged as a `.deb`, started by systemd, see `hack/deb/`) and tunes
local workloads: pinning vCPU threads to cores, steering IRQs of passed-through PCI
devices, switching CPU governors, and publishing the node's CPU/NUMA topology to
Karpenter as a `node-capacity` VM.

---

## 1. Goals & Non-Goals

### Goals

- **One place for "Proxmox, locally".** Every read of `/etc/pve` and every `qm`/`pvesh`/
  `pvecm` invocation lives behind a typed API, instead of being scattered across
  `pkg/proxmox/local`, `pkg/utils/vmconfig` and `cmd/proxmox-scheduler`.
- **A real parser for the PVE section-config format.** The current code unmarshals
  `/etc/pve/qemu-server/<vmid>.conf` with `gopkg.in/yaml.v3`, which is wrong in ways that
  already bite (see §2). The library owns a parser/serializer for the actual *file*
  format: `#` description comments, `key: value` lines, `[PENDING]` and `[<snapshot>]`
  sections.
- **One definition of a guest, shared with `go-proxmox-rest`.** Neither the guest config
  struct nor property-string parsing (`net0: virtio=BC:24:11:…,bridge=vmbr0`) is redefined
  here — both already exist in `go-proxmox-rest` and are reused verbatim. This module owns
  the *file format*; the REST module owns the *types*. See §6.
- **Read direct, write through `qm`.** Reads go straight to the filesystem — cheap,
  lock-free, no fork. Writes never touch `/etc/pve`: they go through `qm set` / `qm create`
  / `qm destroy`, which own locking, schema validation and pmxcfs replication (§7).
- **Testable without a hypervisor.** Two seams — a filesystem root and a command runner —
  let the whole surface be exercised against a temp directory and a scripted `qm`, the
  local counterpart to `go-proxmox-rest`'s `fakeapi`.
- **Idiomatic and consistent with the sibling module.** Same fluent shape
  (`client.Qemu().Config(ctx, vmid)`), same `Option` pattern, same typed errors and
  predicates, same file/package conventions.
- **Tiny dependency footprint.** Exactly one dependency: `go-proxmox-rest`, for the
  shared types and value parsing above. This costs nothing at build time —
  `go list -deps ./nodes/qemu` on the REST module resolves to `internal/params`,
  `internal/property`, `types`, `nodes/qemu/agent`, `nodes/qemu/firewall` and **no resty**
  — so a daemon running as root on a hypervisor still links no HTTP stack. No logging
  framework, no YAML parser.
- **Reusable beyond Karpenter.** A `qm`-wrapping library with a config parser is useful
  to any on-node agent (backup hooks, monitoring, migration tooling).

### Non-Goals (v1)

- Replacing `go-proxmox-rest`. Anything that needs the cluster's view, another node's
  guests, or authentication belongs there. This module never opens a socket.
- Writing `/etc/pve` files directly. All mutations go through `qm`, which owns locking,
  validation and pmxcfs replication (§7).
- Full coverage of every `qm`/`pvesh` subcommand. The design must make adding one cheap.
- LXC support in the first release (the daemon only cares about QEMU guests today).
- Host-level Linux tuning — CPU governors, IRQ affinity, thread pinning. That is
  `pkg/utils/sys`, and it is not Proxmox-specific (§9).

---

## 2. Motivation: what is wrong with the code today

The migration is not a pure move. The existing implementation
(`pkg/proxmox/local`, 6 files, ~250 LOC, plus `pkg/utils/vmconfig`) has defects that the
extraction should fix, and they are the strongest argument for owning a real parser.

**2.1 `description` is never populated — and a feature depends on it.**
PVE does not store the guest description as a `description:` key. It stores it as
`#`-prefixed comment lines at the top of the config file. The example in
`docs/scheduler.md` shows exactly that:

```
#Karpenter discovery service
affinity: 0-7,32-39,…
cores: 64
```

`local.Config.Description` has a `yaml:"description"` tag, so it is always empty. That
makes `vmconfig.LoadVMConfig`'s fallback — "if `affinity` is unset, look for
`affinity=<cpuset>` inside the description" — dead code that has never run.

**2.2 A config with snapshots does not parse.**
`stripPending` truncates at the literal `[PENDING]`. Any other section header a guest
may carry (`[mysnapshot]`) reaches the YAML decoder, which sees a flow sequence where it
expects a mapping key and fails the whole document. `GetVMConfigByFilter` then silently
`continue`s past that VM; `GetVMConfig` returns an error the caller logs and skips.

**2.3 YAML re-interprets PVE's raw strings.**
PVE config values are opaque strings. YAML applies its own rules to them: ` #` starts a
comment mid-line, `on`/`off`/`yes`/`no` become booleans, leading zeros become octal, and
a value containing `: ` splits into a nested mapping. Today's typed struct (six scalar
fields plus ten `hostpciN`) is narrow enough that this mostly does not fire — but it is
luck, not design, and it blocks widening `Config`.

**2.4 The idempotency check in `UpdateVM` compares across types.**
`filterChangedOptions` compares `any` values with `==`. The desired options come from
`buildVMOptions`, where `memory` is `serverInfo.MemoryCapacity / (1024*1024)` — a
`uint64` — while YAML decodes the same number from the file as `int`. `uint64(507904) ==
int(507904)` is false, so `memory` is reported changed on **every** resync (default 60m)
and a `qm set --memory …` runs each time, rewriting the config and bumping its digest for
no reason. The same shape of bug waits for any `int64`/`float64` option added later. (`==`
on `any` also panics outright if either side is an uncomparable type, which YAML can
produce.)

**2.5 No seams.** `qemuServerDir` is a package-level `var` that tests reassign
(`withQemuServerDir`), which rules out `t.Parallel()` and leaks if a test panics. The
`qm`/`pvesh`/`pvecm` calls are unmockable, so the create/update/delete paths and the
quorum wait have zero test coverage.

**2.6 Package-level functions leave no room for policy.** No logger, no timeout, no
dry-run, no alternate root, no binary-path pinning (a root daemon resolving `qm` through
`$PATH` is a small but real privilege-escalation surface).

**2.7 `pvesh`/`pvecm` round-trips where a file would do.** `GetNextID` forks `pvesh get
/cluster/nextid`, which goes through `pvedaemon`; `ClusterReady` forks `pvecm status` and
scrapes human-readable output for `Quorate:`. pmxcfs exposes both as small JSON files
(`/etc/pve/.vmlist`, `/etc/pve/.members`) that are cheaper and far more parse-stable.

**2.8 Layering inversion.** `pkg/utils/vmconfig` (a "utils" package) imports
`pkg/proxmox/local` (a provider package), and `local.Config` then leaks all the way into
`cmd/proxmox-scheduler`'s `updateVMInfo(vmID, pid int, vmConfig *local.Config)`.

**2.9 `GetVMConfigByFilter` semantics are surprising.** Multiple filters are OR-ed, not
AND-ed; iteration order is `os.ReadDir`'s lexical filename order (so `100.conf` precedes
`99.conf`); and unreadable configs are skipped silently, so "not found" and "could not be
read" are indistinguishable.

---

## 3. Scope: what moves, what stays

| Area | Today | Destination |
|---|---|---|
| `pvecm status` / quorum | `pkg/proxmox/local/cluster.go` | **library** — `cluster` |
| VM config file read + parse | `pkg/proxmox/local/vm.go` | **library** — `conf` (file format only) |
| The `Config` struct | `pkg/proxmox/local/types.go` | **`go-proxmox-rest`** — `nodes/qemu.Config`, reused (§6) |
| Property-string parsing | — (absent today) | **`go-proxmox-rest`** — `property.Marshal`/`Unmarshal` (§6.3) |
| `qm create/set/destroy` | `pkg/proxmox/local/vm.go` | **library** — `qemu` |
| `pvesh get /cluster/nextid` | `pkg/proxmox/local/vm.go` | **library** — `cluster` |
| `hostpciN` enumeration | `Config.MergeHostPCIs()` | **`go-proxmox-rest`** — `Config.HostPCI map[int]HostPCI`, already typed |
| vfio-pci cmdline parsing | `pkg/utils/vmconfig/hostpci.go` | **library** — `qemu` (it parses a QEMU cmdline PVE generated); largely obsoleted by typed `HostPCI` |
| `/run/qemu-server/*.pid` scanning | `cmd/proxmox-scheduler/scheduler.go` (`getRunningVMs`, `getVMIDs`) | **library** — `qemu` process discovery |
| `affinity=` fallback from description | `pkg/utils/vmconfig/vmconfig.go` | **split** — parsing in the library, the convention stays in Karpenter (§9) |
| CPU governor, IRQ affinity, thread pinning | `pkg/utils/sys` | **stays** — generic Linux, not Proxmox |
| cadvisor topology, `node-capacity` VM shape | `pkg/utils/systeminfo`, `cmd/proxmox-scheduler/proxmox.go` | **stays** — Karpenter policy |
| fsnotify reconciler | `pkg/utils/reconciler` | **stays** |

Two rules of thumb:

- **If it reads `/etc/pve` or runs a `pve*`/`qm` binary it belongs in the library; if it
  decides *what* to do with the result it stays in Karpenter.**
- **If it describes what a Proxmox value *is*, it belongs in `go-proxmox-rest`** — that
  module already owns the vocabulary, and a second copy on the local side is exactly the
  drift this migration exists to avoid.

---

## 4. Module layout

```
go-proxmox-local
├── go.mod                  // module github.com/sergelogvinov/go-proxmox-local
│                           //   require github.com/sergelogvinov/go-proxmox-rest — types only
├── local.go                // Client, New(), fluent entry points
├── options.go              // WithRoot, WithRunner, WithLogger, WithTimeout, WithDryRun, …
├── exec.go                 // Runner interface + default exec.CommandContext runner
├── errors.go               // typed errors + IsNotFound/IsNoQuorum/IsLocked predicates
├── doc.go
├── conf/                   // the PVE section-config FILE format — nothing about values
│   ├── conf.go             // File, Section, Parse, Format
│   ├── digest.go           // config digest, for optimistic concurrency
│   └── testdata/           // round-trip corpus of real configs
├── cluster/                // pmxcfs + cluster-scope helpers
│   ├── cluster.go          // Quorate, LocalNode, Members, NextID
│   ├── members.go          // /etc/pve/.members
│   ├── vmlist.go           // /etc/pve/.vmlist
│   └── types.go
├── qemu/
│   ├── qemu.go             // Client: Config, List, Find, Create, Update, Delete, Start, Stop
│   ├── config.go           // Config struct (typed subset + indexed families + Extra)
│   ├── process.go          // running guests: /run/qemu-server/*.pid, cmdline, vfio-pci
│   └── types.go
├── lxc/                    // v0.3
├── fakelocal/              // public test double: temp root + scripted runner
├── internal/…
├── docs/design.md          // this document, moved into the new repo
└── examples/
```

Package naming follows the sibling module: the root package is `local`, so the existing
import alias in `cmd/proxmox-scheduler` (`local "…/pkg/proxmox/local"`) keeps working as
`local "github.com/sergelogvinov/go-proxmox-local"`.

### Dependency direction

```
local (root)  ->  stdlib
    ^
    |
cluster / qemu / lxc  ->  local (root), conf,
                          go-proxmox-rest/{types, property, nodes/qemu}
    ^
    |
conf  ->  stdlib          // leaf: no dep on root, on a sibling, or on the REST module
```

Same acyclic rule as `go-proxmox-rest`: subpackages depend only on the root and on the
`conf` leaf, never on each other. `conf` stays a pure-stdlib leaf deliberately — it knows
about lines, sections and `#` comments and nothing about what a value *means*, so it can be
tested and reused independently of the type layer. Root wires children lazily:

```go
func (c *Client) Qemu() *qemu.Client       { return qemu.New(c.env) }
func (c *Client) Cluster() *cluster.Client { return cluster.New(c.env) }
```

where `env` is the small internal struct carrying the resolved root paths, the `Runner`,
the logger and the timeout — the local analogue of `*proxmox.Client` being threaded into
resource packages.

---

## 5. The two seams

Everything this library does is one of two things: **read a file under a known root**, or
**run a PVE binary**. Both get an interface, and that is what makes the module testable.

```go
// Runner executes a PVE CLI tool. The default implementation uses
// exec.CommandContext with an absolute, pre-resolved binary path.
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}
```

```go
// Paths are derived from a single root so a test can point the whole library
// at t.TempDir(). Individual directories can still be overridden.
WithRoot("/")                          // default
WithConfigDir("/etc/pve")              // pmxcfs mount
WithRunDir("/run/qemu-server")         // pid/vnc/qmp sockets
```

Notes on the default `Runner`:

- **Binary paths are resolved once, at `New()`, to absolute paths** (searching the
  standard PVE locations under `/usr/sbin` and `/usr/bin`), not looked up in `$PATH` per
  call. A root daemon should not inherit `$PATH` semantics.
- Every call carries the caller's `ctx`; `WithTimeout` supplies a default deadline when
  the caller's context has none.
- stdout and stderr are captured separately. `qm` puts progress on stdout and errors on
  stderr; folding them together (today's `CombinedOutput`) makes output unparseable.
- `WithDryRun(true)` logs the exact argv and returns success without executing. For a
  daemon that runs as root on someone's hypervisor, this is worth having on day one.

`fakelocal` supplies a `Runner` that records invocations and applies the effect of
`qm set`/`qm create`/`qm destroy` to the fake root's config tree, so a consumer test can
drive a full create-then-update reconcile and assert on the resulting file (§8).

---

## 6. Value parsing and types: reuse, don't reinvent

The boundary between the two modules is sharp, and it falls in the middle of what used to
look like one problem:

> **`go-proxmox-local` owns "config file ⇄ key/value map".
> `go-proxmox-rest` owns "key/value map ⇄ Go struct".**

A guest is a guest whether you learned about it from `pvedaemon` over HTTPS or from
`/etc/pve/qemu-server/100.conf` on the local disk. Only the *transport* differs, and only
the transport should be duplicated.

### 6.1 The `conf` package — file format only

```go
type File struct {
    Description string             // '#' lines joined with '\n', comment markers stripped
    Current     Section            // the active configuration
    Pending     Section            // [PENDING], nil when absent
    Snapshots   map[string]Section // keyed by snapshot name
}

// Section preserves both the key→value mapping and the original key order, so
// Format(Parse(x)) == x for any config PVE wrote.
type Section struct { /* keys []string; values map[string]string */ }

func (sec Section) Map() map[string]string   // the handoff to go-proxmox-rest

func Parse(r io.Reader) (*File, error)
func (f *File) Format(w io.Writer) error
func (f *File) Digest() string               // sha1 of the raw text, PVE's concurrency token
```

Design decisions, each answering a §2 defect:

- **Values are `string`, always — `conf` never interprets one.** No type inference, no
  property-string splitting, no enum validation. That is the next layer's job. (2.3)
- **Description is a first-class field**, reconstructed from the `#` lines. (2.1)
- **Every section is parsed, not truncated.** `Current` is what callers usually want, but
  `Pending` and `Snapshots` are available rather than being a parse failure. (2.2)
- **Round-trip fidelity.** Key order and unknown keys survive.

That is the whole package: a few hundred lines with no dependencies and no opinions.

### 6.2 The typed layer — `go-proxmox-rest/nodes/qemu.Config`

`go-proxmox-rest` already models a QEMU guest completely, and models it *better than this
design originally proposed*:

```go
// nodes/qemu/types.go — excerpt
type Config struct {
    Name        string          `json:"name,omitempty"        url:"name,omitempty"`
    Description string          `json:"description,omitempty" url:"description,omitempty"`
    Tags        *types.Tags     `json:"tags,omitempty"        url:"tags,omitempty"`
    Affinity    string          `json:"affinity,omitempty"    url:"affinity,omitempty"`
    Cores       *int            `json:"cores,omitempty"       url:"cores,omitempty"`
    Memory      *Memory         `json:"memory,omitempty"      url:"memory,omitempty"`
    NUMAEnabled *bool           `json:"numa,omitempty"        url:"numa,omitempty"`
    Digest      string          `json:"digest,omitempty"      url:"digest,omitempty"`

    HostPCI map[int]HostPCI     `json:"-" url:"-"`   // hostpci0…hostpciN, parsed
    NUMA    map[int]NUMA        `json:"-" url:"-"`   // numa0…numa7, parsed
    Net     map[int]Net         `json:"-" url:"-"`
    …
}
```

Every problem a hand-written local `Config` would have to solve is already solved there,
and solved further:

- `HostPCI` is `map[int]HostPCI` — **typed**, not `map[int]string`. `HostPCI.Host` is the
  PCI address the scheduler wants, already extracted from the property string. The
  ten-field `HostPCI0…HostPCI9` enumeration and `MergeHostPCIs()` disappear, and so does
  most of `ParseVfioPciDevices`'s reason to exist.
- `NUMA map[int]NUMA` has `CPUIDs`, `HostNodes`, `Memory`, `Policy` as fields, which is
  exactly what `buildVMOptions` assembles by hand with `fmt.Sprintf` today.
- The `numa` scalar/`numaN` indexed ambiguity is already resolved, and named explicitly:
  `NUMAEnabled` vs. `NUMA`. (Getting this wrong is how
  `pkg/providers/resources/vm/resources.go` ended up deriving NUMA node state for guests
  with `numa: 0` — finding #2 in `docs/review.md`.)
- `Description` is documented there as "saved as a comment inside the config file" — the
  REST struct has known about §2.1 all along; only the local YAML parser did not.
- `Digest` is already a field, which §7.3's concurrency guard needs.

So `qemu.Config` in this module is a **type alias**, the same re-export pattern
`go-proxmox-rest` uses for its own `types` package (`type Rule = types.Rule`):

```go
// qemu/types.go
package qemu

import restqemu "github.com/sergelogvinov/go-proxmox-rest/nodes/qemu"

type Config  = restqemu.Config
type HostPCI = restqemu.HostPCI
type NUMA    = restqemu.NUMA
```

Call sites keep reading `qemu.Config`, and a `*qemu.Config` obtained from the local
filesystem is *the identical Go type* as one fetched over REST — so provider code can be
written once against either source. For `karpenter-provider-proxmox` that is immediate:
`pkg/providers/…` (REST) and `cmd/proxmox-scheduler` (local) stop having two notions of
what a guest config is.

### 6.3 Property strings

`go-proxmox-rest/internal/property` already converts Proxmox comma-separated property
strings to and from tagged structs, using `cfg:"name[,default]"` tags:

```go
func Marshal(v any) (string, error)
func Unmarshal(s string, v any) error
```

It handles the `default` modifier (a leading bare value such as `virtio=…` in `net0`, or
the raw address in `hostpci0`), `;`-joined slices, `1`/`0` booleans and pointer fields.
This module reuses it as-is. It does **not** get a second implementation in `conf`.

### 6.4 Required upstream changes in `go-proxmox-rest`

Two small, self-contained changes, both worth making on their own merits. They are
prerequisites for this module and should land first (§12, Phase 0.5).

**(a) Promote `internal/property` to a public `property` package.** Move the directory;
the API is already clean and documented. `internal/` is the only thing keeping it out of
reach, and property strings are exactly the kind of thing an on-node tool needs.

**(b) Export a string-valued config codec from `nodes/qemu`.** `decodeConfig` and
`encodeConfig` already exist and are exactly right — they are merely unexported, and
`decodeConfig` takes `map[string]json.RawMessage` because it was written for the REST
path:

```go
// nodes/qemu/config.go — new exported surface
func DecodeConfig(raw map[string]string) (*Config, error)
func EncodeConfig(cfg *Config) (map[string]string, error)
```

`EncodeConfig` is a straight rename of `encodeConfig`, which already returns
`map[string]string` — i.e. already returns `qm set --key value` arguments.

`DecodeConfig` is a thin adapter: JSON-quote each value and delegate to the existing
`decodeConfig`. This works without touching `params.Decode`, because that decoder
*already* coerces JSON strings into numeric and boolean fields — it had to, since "Proxmox
encodes numeric fields inconsistently across endpoints" (its own doc comment). The
config-file case is just one more caller of a tolerance that already exists. `types.Tags`
likewise already has an `UnmarshalJSON` that splits on `;`, so `tags: karpenter;test`
decodes correctly with no new code.

The indexed-family path needs nothing at all: `decodeConfig` already unmarshals indexed
values into a `string` before calling `setIndexedField(cfg, prefix, index, v)`.

A round-trip test belongs upstream with the change: `DecodeConfig(EncodeConfig(cfg))`
≡ `cfg`, plus a fixture decoded both from a real API response and from the equivalent
config file, asserting the two `*Config` values are equal. That test is the contract
between the two modules, and it lives where the types do.

### 6.5 The cost: pointer fields

Being honest about the trade-off. `qemu.Config` uses pointers (`*int`, `*bool`, `*Memory`)
so that writes can distinguish "unchanged" from "explicitly zero" — correct for the REST
module's purposes, and §6 of its own design doc explains why. Read-mostly local callers pay
for it:

```go
// today
if vmConfig.Cores == cpus.Size() { … }

// after
if lo.FromPtr(cfg.Cores) == cpus.Size() { … }
```

`Memory` is the sharper edge: it becomes `*Memory` with a `Current *int` field (PVE 9's
`memory: current=4096,max=…`), so `vmConfig.Memory` is a two-level dereference.

This is worth paying — one shared, complete, tested struct beats a second hand-maintained
subset. `karpenter-provider-proxmox` already depends on `samber/lo`, so `lo.FromPtr` is
idiomatic at those call sites. If the pattern gets noisy, the fix is a few accessors in
this module's `qemu` package (`func CoresOf(*Config) int`), not a divergent struct.

---

## 7. The read/write asymmetry

**Reads go direct. Writes go through `qm`.** This is the module's central operating rule,
and it is not a detail of one method — it is why the module is shaped the way it is.

### 7.1 Reads: straight to the filesystem

`/etc/pve` is a FUSE mount over a replicated database, but reading it is an ordinary file
read: no fork, no lock, no `pvedaemon` round-trip, microseconds rather than tens of
milliseconds. For a daemon that reacts to every guest start via fsnotify and re-scans every
running guest on each resync, that difference is the whole reason this module exists
instead of a REST call to `localhost`.

So: `Config`, `List`, `Find`, `Running`, `Quorate`, `LocalNode`, `Members`, `NextID` all
read files. No read path in this library forks a process, except as an explicit fallback
when a pmxcfs file is absent or unparseable (§12, Phase 4).

### 7.2 Writes: never touch `/etc/pve`

pmxcfs holds a per-file write lock and replicates every commit to the cluster; `qm`
additionally takes `/var/lock/qemu-server/lock-<vmid>.conf` and validates values against
the PVE schema before writing. A library that wrote the file itself would be racing both
mechanisms, and could replicate a config cluster-wide that `pvedaemon` then rejects.

**`qm set` is the default and the only supported write path.** `conf.Format` exists for
round-trip tests and for diffing — not as a write path, and the package documentation
should say so in those words.

Consequences:

- `Update` → `qm set <vmid> --key value …`, `Create` → `qm create`, `Delete` → `qm destroy`.
- The arguments come from `qemu.EncodeConfig(cfg)` (§6.4b), which already returns exactly
  the `map[string]string` that `qm` wants — the same encoder the REST module uses to build
  its PUT body. One encoder, two transports.
- The library must recognise `qm`'s lock-contention failure and surface it as a typed,
  retryable `ErrLocked` rather than an opaque exit status.
- `WithDryRun(true)` logs the argv and skips execution — cheap to support precisely
  because there is exactly one write path to intercept.

### 7.3 Correct change detection

Replacing `filterChangedOptions` (2.4):

```go
func (c *Client) Update(ctx context.Context, vmid int, cfg *Config) error
```

1. Read and parse the current config (`conf.Parse`) — a direct read, per §7.1.
2. Encode both the current and the desired config to `map[string]string` via
   `qemu.EncodeConfig`.
3. Diff **string against string**, never `any == any`. Both sides went through the same
   encoder, so `uint64(507904)` and `int(507904)` are both `"507904"` and the spurious
   hourly `qm set --memory` of 2.4 cannot recur — by construction, not by careful coding at
   the call site.
4. Property-string values compare equal regardless of key order, because `property.Marshal`
   emits fields in struct order on both sides.
5. If nothing differs, return without invoking `qm`. Otherwise run `qm set` with only the
   differing keys.

Routing both sides through one encoder is what makes step 3 safe, and is the second
concrete payoff from sharing types rather than maintaining a local subset.

### 7.4 Optimistic concurrency

PVE's config digest is the SHA-1 of the raw config text, and `qm set` accepts
`--digest <sha1>` to reject a write if the file changed since it was read. Since step 1
already read the file, the library can compute `conf.File.Digest()` and pass it — closing
the read-modify-write race against a concurrent `qm set` or a web-UI edit. `qemu.Config`
already carries a `Digest` field for the REST equivalent of this.

A `WithDigestChecks(false)` escape hatch covers PVE versions or subcommands where this
turns out not to hold; the behaviour should be confirmed against a live node before it is
enabled by default.

---

## 8. Testing

Mirroring `go-proxmox-rest`'s `fakeapi`, the local module ships a public `fakelocal`
package so that *consumers* — `cmd/proxmox-scheduler` above all — can be tested without a
hypervisor.

```go
func TestSchedulerCreatesCapacityVM(t *testing.T) {
    fake := fakelocal.New(t,
        fakelocal.WithNode("pve1"),
        fakelocal.WithQuorum(true),
        fakelocal.WithGuest(100, "name: other-vm\ntags: unrelated\n"),
    )

    client := fake.Client(t)   // a *local.Client rooted at the fake tree

    // … drive the code under test …

    fake.AssertRan(t, "qm", "create", "101", "--name", "node-capacity")
    cfg := fake.Guest(t, 101)              // *qemu.Config, decoded from the fake tree
    require.Equal(t, types.Tags{"karpenter"}, *cfg.Tags)
}
```

What the fake provides:

- A temp root with `etc/pve/.members`, `etc/pve/.vmlist`, `etc/pve/qemu-server/*.conf`,
  `etc/pve/nodes/<node>/…` and `run/qemu-server/*.pid`.
- A `Runner` that records every argv and interprets `qm create`/`set`/`destroy`,
  `pvesh get /cluster/nextid` and `pvecm status` against that tree, so a
  create-then-update sequence behaves like a real node.
- Fault injection: make a command fail, return a lock error, or hang, to exercise the
  retry paths that are untested today.

Beyond that: table-driven tests for `conf.Parse`/`Format` round-trips over a corpus of
real configs in `testdata/` (including snapshots, `[PENDING]`, multi-line descriptions and
awkward values). Property-string and struct-mapping tests are *not* duplicated here — they
belong upstream with the code, alongside the §6.4 cross-transport round-trip test, which is
the real contract between the two modules. The existing `TestParseQuorate`,
`TestStripPending`, `TestGetVMConfig*` and `TestParseVfioPciDevices` cases port over as
the seed corpus — with the package-level-var trick (2.5) replaced by a per-test root, which
makes them `t.Parallel()`-safe.

---

## 9. What stays behind in Karpenter, and why

- **`pkg/utils/sys`** — `PinThreadsToCores`, `SetCPUGovernor`, `SetPciIRQAffinity`,
  `GetPciDeviceIRQs`, `GetProcessThreads`. These touch `/sys`, `/proc` and `sched_setaffinity`.
  Nothing about them is Proxmox-specific; a future `go-linux-sys` could claim them, but
  they do not belong in a Proxmox library.
- **`pkg/utils/systeminfo` + cadvisor topology** — hardware discovery, unrelated to PVE.
- **The `node-capacity` VM contract** — the name, the `karpenter` tag, the
  `affinity`/`numa<N>` encoding of host topology (`buildVMOptions`) is a Karpenter-provider
  convention, documented in `docs/scheduler.md`. The library supplies `Create`/`Update`;
  the provider decides what to create.
- **The `affinity=` in-description fallback** — splits in two. Making `Description`
  actually available is the library's job (2.1); interpreting `affinity=<cpuset>` inside
  it is a Karpenter convention and stays in the provider, next to the code that consumes
  the cpuset.

After the migration `pkg/proxmox/local` and `pkg/utils/vmconfig` are deleted; what remains
of `LoadVMConfig` is a ~15-line helper in `cmd/proxmox-scheduler`.

---

## 10. Error handling

```go
var (
    ErrNotFound     = errors.New("guest not found")       // no config file for that vmid
    ErrNoQuorum     = errors.New("cluster has no quorum")
    ErrNotInstalled = errors.New("proxmox tooling not found")  // qm/pvesh/pvecm missing
    ErrLocked       = errors.New("guest config is locked")     // retryable
)

// CommandError carries what a PVE CLI invocation actually did.
type CommandError struct {
    Cmd      string
    Args     []string
    ExitCode int
    Stderr   string
}

// ConfigError carries the file and line a parse failed on.
type ConfigError struct { Path string; Line int; Err error }

func IsNotFound(err error) bool
func IsNoQuorum(err error) bool
func IsLocked(err error) bool     // callers should retry with backoff
```

`ErrNotInstalled` matters for the consumer's test and dev environments: the karpenter
controller binary and the e2e suite do not run on a hypervisor, and "the tool is absent"
should be distinguishable from "the command failed".

Errors wrap with `%w` throughout; `context.Canceled`/`DeadlineExceeded` propagate
unmodified.

---

## 11. API sketch

```go
client, err := local.New(
    local.WithLogger(logger),
    local.WithTimeout(30*time.Second),
)
if err != nil { … }

// cluster
ok, err := client.Cluster().Quorate(ctx)          // /etc/pve/.members, no fork
node, err := client.Cluster().LocalNode(ctx)      // this node's name
id,  err := client.Cluster().NextID(ctx)          // /etc/pve/.vmlist, pvesh fallback

// qemu — config (direct filesystem reads)
cfg,  err := client.Qemu().Config(ctx, 100)       // *qemu.Config = *restqemu.Config
list, err := client.Qemu().List(ctx)              // []int, numerically sorted
vmid, cfg, err := client.Qemu().Find(ctx, func(c *qemu.Config) bool {
    return c.Name == "node-capacity" && slices.Contains(*c.Tags, "karpenter")
})
file, err := client.Qemu().ConfigFile(ctx, 100)   // *conf.File: sections, order, digest

// qemu — lifecycle (always via qm)
err = client.Qemu().Create(ctx, vmid, cfg)        // qm create
err = client.Qemu().Update(ctx, vmid, cfg)        // qm set, diffed + digest-guarded
err = client.Qemu().Delete(ctx, vmid)             // qm destroy

// qemu — running processes
running, err := client.Qemu().Running(ctx)        // map[vmid]pid from /run/qemu-server
devs,    err := client.Qemu().VfioPCIDevices(ctx, pid)
```

`Find` takes a single `func(*Config) bool` — no error return, no variadic OR — which
removes 2.9's surprises. Callers that want AND compose it themselves; callers that want
every match use `List` plus `Config`.

`Create`/`Update` take a `*Config` rather than a `map[string]any`: the same struct reads
and writes, encoded by `qemu.EncodeConfig`. That is what makes §7.3's diff sound, and it
means a caller can read a config, change two fields and write it back without ever naming
a PVE key as a string. `ConfigFile` is the escape hatch for callers that need the raw
sections, key order or an unmodelled key.

### Symbol mapping

| Today | New |
|---|---|
| `local.ClusterReady(ctx)` | `client.Cluster().Quorate(ctx)` |
| `local.GetNextID(ctx)` | `client.Cluster().NextID(ctx)` |
| `local.GetVMConfig(vmID)` | `client.Qemu().Config(ctx, vmid)` |
| `local.GetVMConfigByFilter(f…)` | `client.Qemu().Find(ctx, pred)` |
| `local.CreateVM/UpdateVM/DeleteVM` | `client.Qemu().Create/Update/Delete` |
| `local.Config.MergeHostPCIs()` | `cfg.HostPCI` (`map[int]qemu.HostPCI`, typed) |
| `local.Config` (16 fields) | `qemu.Config` = `go-proxmox-rest/nodes/qemu.Config` (alias) |
| `local.ErrVirtualMachineNotFound` | `local.ErrNotFound` |
| `vmconfig.ParseVfioPciDevices(argv)` | `client.Qemu().VfioPCIDevices(ctx, pid)` — or drop it: `cfg.HostPCI[n].Host` is the same address, from the config rather than the cmdline |
| `vmconfig.LoadVMConfig(vmID)` | `client.Qemu().Config` + provider-local affinity fallback |
| `scheduler.go:getRunningVMs/getVMIDs` | `client.Qemu().Running(ctx)` |

---

## 12. Migration plan

**Phase 0 — bootstrap the repo.** Create `go-proxmox-local` from the `go-proxmox-rest`
skeleton: `LICENSE` (Apache-2.0), `Makefile`, `.golangci.yml`, `.conform.yaml`,
`.github/workflows`, `.devcontainer`, `docs/design.md` (this document). `go.mod` declares
`go 1.26.0` to match the sibling and requires `go-proxmox-rest` — and nothing else.

**Phase 0.5 — the upstream changes, in `go-proxmox-rest` (§6.4).** Promote
`internal/property` to `property`, and export `DecodeConfig(map[string]string)` /
`EncodeConfig(*Config)` from `nodes/qemu` with the round-trip test. Both are small,
independently useful, and block everything below. Tag a release.

**Phase 1 — port with seams, keep behaviour.** Move `cluster.go`, `vm.go`, `types.go`,
`errors.go` and `hostpci.go` in, restructured into `local`/`cluster`/`qemu`, with the
`Runner` and root-path seams wired in and the existing tests carried over. The parse stays
as it is here; the point of this phase is that the *shape* of the module — packages,
seams, client chain, errors — is reviewable on its own, before any behaviour changes.

**Phase 2 — the `conf` parser and the type switch.** Write `conf` against a corpus of real
configs; replace the local `Config` struct with the alias to `go-proxmox-rest`'s, wiring
`conf.Section.Map()` → `qemu.DecodeConfig` on the read path and `qemu.EncodeConfig` →
`qm set` on the write path. Fixes 2.1–2.4. This is where the behaviour actually changes,
and it should land as its own PR with the round-trip corpus.

**Phase 3 — switch the provider over.** In `karpenter-provider-proxmox`:

1. Add the dependency, with a commented `replace` line for local development next to the
   two that already exist in `go.mod`:
   ```
   // replace github.com/sergelogvinov/go-proxmox-local => ../../proxmox/go-proxmox-local
   ```
2. Rewrite `cmd/proxmox-scheduler/proxmox.go`, `vm.go` and `scheduler.go` against the new
   API; `updateVMInfo` takes `*qemu.Config`. `buildVMOptions` stops assembling
   `map[string]any` with `fmt.Sprintf` and builds a `*qemu.Config` with typed `NUMA`
   entries instead — the single largest readability win of the migration, and the one
   that retires 2.4's class of bug at the source.
3. Expect pointer-dereference churn at the call sites (§6.5): `cfg.Cores`, `cfg.Memory`
   and `cfg.NUMAEnabled` are pointers. `lo.FromPtr` is already a dependency.
4. Delete `pkg/proxmox/local` and `pkg/utils/vmconfig`, keeping the affinity-fallback
   helper in `cmd/proxmox-scheduler`.
5. `make vendor` (`go mod tidy && go mod vendor`) — the repo vendors, so this is not
   optional. Confirm the vendor tree gained only `go-proxmox-local` and, at most,
   `go-proxmox-rest/{property, nodes/qemu/*, types}` — no new external module.
6. Add a `fakelocal`-based test for the `node-capacity` create/update path, which has no
   coverage today.

**Phase 4 — the local-first reads.** Replace `pvecm status` with `/etc/pve/.members` and
`pvesh get /cluster/nextid` with `/etc/pve/.vmlist`, keeping the fork as a fallback when
the file is absent or unparseable. Behaviour-preserving, so it can land independently.

**Phase 5 — beyond Karpenter.** LXC, `qm start/stop/reboot`, storage helpers (`pvesm`),
node fencing state. Driven by demand, not up front.

Phase 0.5 gates everything. After it, Phases 1–2 and 3 can proceed in parallel against a
`replace` directive; only Phase 3's merge needs a tagged version of each module.

---

## 13. Open questions

1. ~~Share types with `go-proxmox-rest`?~~ **Decided: yes** (§6). The feared cost — a local
   library dragging in an HTTP stack — does not exist: `nodes/qemu` has no resty in its
   transitive dependencies. In exchange the module drops its own `Config`, its own
   property-string parser and its own indexed-family table, and a `*qemu.Config` read from
   disk becomes the same Go type as one fetched over REST. The remaining cost is
   §6.5's pointer fields, and the remaining risk is version coupling: `go-proxmox-local`
   must track `go-proxmox-rest` releases, and a breaking change to `qemu.Config` is now a
   breaking change to both modules. Both are acceptable; the alternative was two
   definitions drifting apart, which `docs/review.md` finding #2 shows is not theoretical.
2. **Should `qemu.Config` eventually move into a leaf package?** `nodes/qemu` carries a
   REST `Client` alongside the types. It imports nothing heavy today, so this is not
   urgent — but if it ever grows a transport dependency, the config types should move to
   `types/qemu.go` (which already exists) and `nodes/qemu` should alias them back, exactly
   the pattern that module already applies to its firewall and Ceph types.
3. **Should `Update` fall back to `qm set` without `--digest`** when the digest is rejected,
   or surface the conflict? Surfacing it is the safer default; the daemon retries on the
   next resync anyway.
4. **How much of `pkg/utils/sys` is genuinely generic?** `SetPciIRQAffinity(vmID, …)` takes
   a VM ID purely for logging. If more of these grow PVE-shaped arguments, the boundary
   drawn in §9 should be revisited rather than quietly eroded.
5. **Does `qm set --digest` behave as assumed on the PVE versions we target?** §7.4 needs
   confirmation on a live node before the digest guard is enabled by default.

---

## Appendix: PVE local surface reference

| Path / tool | Contents | Used for |
|---|---|---|
| `/etc/pve/qemu-server/<vmid>.conf` | this node's QEMU guest configs | config read |
| `/etc/pve/lxc/<vmid>.conf` | this node's containers | v0.3 |
| `/etc/pve/nodes/<node>/qemu-server/` | any node's guest configs | cross-node reads |
| `/etc/pve/local` | symlink to `/etc/pve/nodes/<this node>` | local node name |
| `/etc/pve/.members` | JSON: node list, quorum state | `Quorate`, `LocalNode`, `Members` |
| `/etc/pve/.vmlist` | JSON: every vmid → owning node | `NextID`, ownership |
| `/etc/pve/.version` | pmxcfs file versions | change detection |
| `/run/qemu-server/<vmid>.pid` | pid of a running guest | running-guest discovery |
| `/var/lock/qemu-server/lock-<vmid>.conf` | `qm`'s flock | lock-contention detection |
| `qm` | guest lifecycle and config | all writes |
| `pvesh` | local CLI access to the API tree | fallbacks |
| `pvecm` | cluster membership | quorum fallback |
| `pvesm` | storage | v0.4 |
