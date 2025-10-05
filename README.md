# ssh-hypervisor

_Like SimCity, but for virtual machines!_

![Screen recording of using ssh-hypervisor](https://i.imgur.com/AxtMjJL.gif)

`ssh-hypervisor` is an SSH server that dynamically provisions Linux microVMs with [Firecracker](https://github.com/firecracker-microvm/firecracker). Once set up, you can just SSH into it from anywhere to instantly allocate a fresh VM.

```bash
# Dynamically create a microVM, or restore from past state.
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
- [iproute2](https://en.wikipedia.org/wiki/Iproute2) – the `ip` command
- [iptables](https://en.wikipedia.org/wiki/Iptables)

<!-- Idle VMs are automatically suspended with a [snapshot](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) that is stored on disk. If the same user logs in within a time period, they receive a snapshot of the previous VM state that gets resumed. -->

## Development

To build the project:

```bash
# Download platform-specific Firecracker binary
go generate ./...

# Build the binary (static linking, no CGO dependencies)
CGO_ENABLED=0 go build ./cmd/ssh-hypervisor

# Grant required CAP_NET_ADMIN to the binary
sudo setcap cap_net_admin+ep ./ssh-hypervisor
```

To run tests, just use `go test` directly.

```bash
go test -v -exec sudo ./...
```

Then, you will need to build a rootfs once, and run the server:

```bash
# Build a rootfs, requires docker. Produces 'rootfs.ext4' file.
scripts/create-rootfs.sh

./ssh-hypervisor -rootfs rootfs.ext4
```

Your user must be able to access `/dev/kvm`, e.g., by being in the `kvm` group. VMs may not have Internet access by default unless you pass `-allow-internet`.
