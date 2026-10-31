module github.com/sergelogvinov/go-proxmox-local

go 1.27.1

// replace github.com/sergelogvinov/go-proxmox-rest => ../go-proxmox-rest

require (
	github.com/fsnotify/fsnotify v1.10.1
	github.com/sergelogvinov/go-proxmox-rest v0.1.2-0.20261027091835-894de17e4888
	github.com/stretchr/testify v1.12.1
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.13.0 // indirect
)
