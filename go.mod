module github.com/sergelogvinov/go-proxmox-local

go 1.27.1

// replace github.com/sergelogvinov/go-proxmox-rest => ../go-proxmox-rest

require (
	github.com/sergelogvinov/go-proxmox-rest v0.0.0-20260924110310-ba544056d7ef
	github.com/stretchr/testify v1.12.1
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect
