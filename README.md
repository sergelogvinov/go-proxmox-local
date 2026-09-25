# Proxmox Local

> **Status:** This project is under active development. Its API may change and
> breaking changes are possible. Do not use it in production yet.

`go-proxmox-local` is a Go library for driving a Proxmox VE hypervisor **from the node
itself** — reading `/etc/pve` directly and shelling out to `qm` / `pvesh` / `pvecm` — as a
sibling to [`go-proxmox-rest`](https://github.com/sergelogvinov/go-proxmox-rest) (the REST
client) and [`go-proxmox-pool`](https://github.com/sergelogvinov/go-proxmox-pool)
(cluster-wide scheduling helpers).

See [`docs/design.md`](docs/design.md) for the full design.

## Projects using this module

- [Karpenter for Proxmox](https://github.com/sergelogvinov/karpenter-provider-proxmox)

## License

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

---

`Proxmox®` is a registered trademark of [Proxmox Server Solutions GmbH](https://www.proxmox.com/en/about/company).
