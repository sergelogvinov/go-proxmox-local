# Changelog

## Unreleased


### Features

* **qemu:** add `Qemu().Watch`, a blocking loop that turns the run dir into checked VM start, VM stop and running-guests events (`EventHandler`, `WatchOptions`); `fakelocal` gains `SetRunning`, `SetStopped`, `WritePIDFile` and `WriteProcess` to drive it

## [0.2.0](https://github.com/sergelogvinov/go-proxmox-local/compare/v0.1.0...v0.2.0) (2026-10-31)


### Features

* **qemu:** watch the run dir for guest start and stop events ([d605eef](https://github.com/sergelogvinov/go-proxmox-local/commit/d605eefa27869ce405586b718195c516f39f1c4f))
* report whether a QEMU guest runs ([db06392](https://github.com/sergelogvinov/go-proxmox-local/commit/db063922fa42b439b277fa523de375252484083a))


### Bug Fixes

* encode indexed config families through params ([66bbb88](https://github.com/sergelogvinov/go-proxmox-local/commit/66bbb88ec28d0017a0612cbbbc4c595a8a34e7a0))
* gh actions ([e7040ad](https://github.com/sergelogvinov/go-proxmox-local/commit/e7040adb80cdff3e458ba05a921507c7f806bd01))
* **qemu:** keep a pending VM start across a sync ([6cc6800](https://github.com/sergelogvinov/go-proxmox-local/commit/6cc6800b5742b9c0a094b56a3f59ce13f1a56312))
* **qemu:** report a VM start a sync finds first ([c31a208](https://github.com/sergelogvinov/go-proxmox-local/commit/c31a20806ddb3233e39796371282cc4a431d8be0))

## [0.1.0](https://github.com/sergelogvinov/go-proxmox-local/compare/v0.0.1...v0.1.0) (2026-09-25)


### Features

* add fakelocal package and tests for Proxmox local client simulation ([5eb4b28](https://github.com/sergelogvinov/go-proxmox-local/commit/5eb4b284ee87b618205e2e2ae1198d11ae367d12))
* add LXC container management functionality ([3194092](https://github.com/sergelogvinov/go-proxmox-local/commit/3194092d212a6d8bacb9e5845640984101aa9b34))
* implement QEMU guest configuration management ([0aacfa4](https://github.com/sergelogvinov/go-proxmox-local/commit/0aacfa44f9917e3b89c86bfa32c2eb538d53611d))
* initial commit ([cb89065](https://github.com/sergelogvinov/go-proxmox-local/commit/cb89065f0fb0c44c0b38d77e9e4542ba254585cb))
