# Script to create a rootfs for hypervisor use. You can customize this!

set -euo pipefail

dd if=/dev/zero of=rootfs.ext4 bs=1M count=50  # 50 MB disk image
mkfs.ext4 rootfs.ext4
rootfs_dir="$(mktemp -d -p "$PWD" rootfs.XXXX)"
chmod 755 "$rootfs_dir"
sudo mount rootfs.ext4 "$rootfs_dir"

cleanup() {
  if [[ -n "${rootfs_dir:-}" && -d "$rootfs_dir" ]]; then
    sudo umount "$rootfs_dir" 2>/dev/null || true
    rmdir "$rootfs_dir"
  fi
}

trap cleanup EXIT

# Start a container to copy files into the rootfs image
docker run -i --rm \
  -v "$rootfs_dir":/my-rootfs \
  alpine sh <<EOS
set -euo pipefail

# NOTE: We gave up on openrc, just going to use sh as an init process now.
# apk add --no-cache openrc

apk add --no-cache util-linux openssh rng-tools

# Set up a login terminal on the serial console (ttyS0):
# ln -s agetty /etc/init.d/agetty.ttyS0
# echo ttyS0 > /etc/securetty
# rc-update add agetty.ttyS0 default

# Make sure special file systems are mounted on boot:
# rc-update add devfs boot
# rc-update add procfs boot
# rc-update add sysfs boot
# rc-update add localmount boot
# echo "devpts  /dev/pts  devpts  defaults,gid=5,mode=620,ptmxmode=666  0  0" >> /etc/fstab

# Provide entropy, otherwise sshd will hang
# rc-update add rngd boot
# echo 'RNGD_OPTS="-r /dev/urandom"' >> /etc/conf.d/rngd  # TODO: Is this needed?

# rc-update add sshd default

# Remove the message of the day
rm /etc/motd

# Generate SSH host keys
ssh-keygen -A

# Enable SSH root login without password
passwd -d root
sed -i 's/^#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^#PermitEmptyPasswords.*/PermitEmptyPasswords yes/' /etc/ssh/sshd_config

# Create the custom init script
cat >/sbin/init-sshvm <<'EOF'
#!/bin/sh
set -euo pipefail
mkdir -p /var/empty /var/log /dev/pts
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devpts devpts /dev/pts
rngd -f -r /dev/urandom &
/usr/sbin/sshd -D -e
EOF
chmod +x /sbin/init-sshvm

# Then, copy the newly configured system to the rootfs image:
for d in bin etc lib root sbin usr; do tar c "/\$d" | tar x -C /my-rootfs; done

# The above command may trigger the following message:
# tar: Removing leading "/" from member names
# However, this is just a warning, so you should be able to
# proceed with the setup process.

for dir in dev proc run sys var; do mkdir /my-rootfs/\${dir}; done
EOS

echo "Rootfs image created successfully: rootfs.ext4"
