# Script to create a rootfs for hypervisor use. You can customize this!

dd if=/dev/zero of=rootfs.ext4 bs=1M count=50  # 50 MB disk image
mkfs.ext4 rootfs.ext4
rootfs_dir="$(mktemp -d -p "$PWD" rootfs.XXXX)"
chmod 755 "$rootfs_dir"
sudo mount rootfs.ext4 "$rootfs_dir"

# Start a container to copy files into the rootfs image
docker run -i --rm \
  -v "$rootfs_dir":/my-rootfs \
  alpine sh <<EOF
apk add --no-cache openrc
apk add --no-cache util-linux openssh

# Set up a login terminal on the serial console (ttyS0):
ln -s agetty /etc/init.d/agetty.ttyS0
echo ttyS0 > /etc/securetty
rc-update add agetty.ttyS0 default

# Make sure special file systems are mounted on boot:
rc-update add devfs boot
rc-update add procfs boot
rc-update add sysfs boot
rc-update add dmesg boot
rc-update add mdev boot

rc-update add sshd default

# Generate SSH host keys
ssh-keygen -A

# Set root password to "root"
echo "root:root" | chpasswd

# Enable SSH root login with password
sed -i 's/^#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config

# Then, copy the newly configured system to the rootfs image:
for d in bin etc lib root sbin usr; do tar c "/\$d" | tar x -C /my-rootfs; done

# The above command may trigger the following message:
# tar: Removing leading "/" from member names
# However, this is just a warning, so you should be able to
# proceed with the setup process.

for dir in dev proc run sys var; do mkdir /my-rootfs/\${dir}; done
EOF

sudo umount "$rootfs_dir"
rmdir "$rootfs_dir"

echo "Rootfs image created successfully: rootfs.ext4"
