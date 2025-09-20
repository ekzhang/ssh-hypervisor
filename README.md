# ssh-hypervisor

_Like SimCity, but for virtual machines!_

`ssh-hypervisor` is an SSH server that dynamically provisions Linux microVMs with [Firecracker](https://github.com/firecracker-microvm/firecracker). Once set up, you can just SSH into it from anywhere to instantly allocate a fresh VM.

```bash
# Dynamically create a VM, or restore past state from snapshot.
ssh yourname@vmcity.ekzhang.com

$ whoami  # You are now SSH'd into the VM!
```

Just for fun! Not intended to be used in production at this time.

I could see it potentially becoming a useful building block for provisioning lightweight VMs, since many languages have SSH client libraries. Let's discuss if you want to make this happen.

## Usage

The `ssh-hypervisor` binary is statically linked and written in Go.

System requirements:

- [Linux](https://en.wikipedia.org/wiki/Linux) running [x86-64](https://en.wikipedia.org/wiki/X86-64) or [ARM64](https://en.wikipedia.org/wiki/AArch64) architectures
- [KVM](https://linux-kvm.org/page/Main_Page) – check `stat /dev/kvm`

Idle VMs are automatically suspended with a [snapshot](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) that is stored on disk. If the same user logs in within a time period, they receive a snapshot of the previous VM state that gets resumed.

## Development

To build the project:

```bash
# Download platform-specific Firecracker binary
go generate ./...

# Build the binary (static linking, no CGO dependencies)
CGO_ENABLED=0 go build ./cmd -o ssh-hypervisor
```

To run tests, just use `go test` directly.

```bash
go test -v ./...
```
