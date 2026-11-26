# Tuning: kernel parameters and swap

## Overview

The `tuning` module sets a fixed list of kernel parameters, now and at boot,
and manages one swap file, `/darkpanel.swap`. A parameter change applies at
once and then waits for your confirmation; unconfirmed, it reverts by itself,
as a firewall change does.

The module is on for every host. Reads need the `read` scope; every change
needs `admin`.

## Before you start

- A swap file needs an ext4 or xfs root file system. On any other, such as
  btrfs, the swap part is unavailable and names the file system; the swap
  areas are still listed.
- Swap that Darkpanel did not create, such as a swap partition, is shown
  read-only.

## Use it in the web UI

The **Tuning** tab has two parts.

**Kernel parameters** lists each parameter with its **Live** value, the value
Darkpanel set (**Darkpanel**) and a **Value at boot** field. A value outside
its range is refused inline, for example "Use 0 to 200" for `vm.swappiness`.
A parameter another module manages is locked and says so, such as **Managed by
firewall**. When a file read later at boot sets the same parameter, the row
warns, naming the file, that it sets the parameter later and wins. One
**Review changes** button plans every changed parameter as one change, with a
count of what changed; the dialog applies it and then counts down to the
deadline, and **Confirm** keeps it.

**Swap** shows each swap area with its use as a bar and the host's memory, or
**No swap is on.** **Create swap file** makes `/darkpanel.swap` at the size you
give. For an existing one, **Resize** takes a new size in MiB and **Remove**
turns it off and deletes it. A size outside the free space is refused inline
with the range.

## Use it from the CLI

Commands reach the controller with an API token, which [Create the first
admin](../first-admin.md#next-an-api-token-for-the-cli) explains how to
create. Each command below is the generated command of one action, and the
[tuning API reference](../../api/tuning.md) lists its input and output.
`--host <id or name>` picks the host.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `tuning.sysctl.get` | `darkpanel tuning sysctl get` | Reads each parameter's live value, the value Darkpanel set, and any later file that sets it. | Nothing; it only reads. |
| `tuning.sysctl.apply` | `darkpanel tuning sysctl apply` | Sets parameters now and in Darkpanel's file. | Your confirmation before the deadline, or it reverts. |
| `tuning.sysctl.repair` | `darkpanel tuning sysctl repair` | Rewrites Darkpanel's file and the live values from the last confirmed settings. | Nothing. |
| `tuning.swap.get` | `darkpanel tuning swap get` | Lists the swap areas and memory, and whether Darkpanel can make a swap file here. | Nothing; it only reads. |
| `tuning.swap.create` | `darkpanel tuning swap create` | Creates `/darkpanel.swap`, turns it on and adds it to `/etc/fstab`. | Nothing. Undo with `change undo`, which removes it. |
| `tuning.swap.resize` | `darkpanel tuning swap resize` | Turns `/darkpanel.swap` off, rewrites it at the new size and turns it on. | Nothing. Undo with `change undo`. |
| `tuning.swap.remove` | `darkpanel tuning swap remove` | Turns `/darkpanel.swap` off, removes it from `/etc/fstab` and deletes it. | Nothing. Undo with `change undo`. |

```sh
darkpanel tuning sysctl get --host web-1
darkpanel tuning sysctl apply --host web-1 --input sysctl.json --confirm-timeout 120
darkpanel change confirm <change-id>
darkpanel tuning swap create --host web-1 --size-mi-b 2048
```

`tuning sysctl apply` takes its values in a JSON document with `--input`. The
document names the whole of Darkpanel's file: a parameter left out is removed
from the file and keeps its live value, and an empty `values` object removes
the file.

```json
{"values": {"vm.swappiness": "10", "net.core.somaxconn": "8192", "net.ipv4.ip_local_port_range": "10000 65000"}}
```

The change waits for confirmation, 60 seconds unless `--confirm-timeout` says
otherwise, and puts the previous values back at its deadline.

### Kernel parameters

| Parameter | Values |
|---|---|
| `vm.swappiness` | 0 to 200 |
| `vm.vfs_cache_pressure` | 1 to 1000 |
| `vm.dirty_ratio` | 1 to 100 |
| `vm.dirty_background_ratio` | 1 to 100 |
| `vm.overcommit_memory` | 0 to 2 |
| `vm.max_map_count` | 65530 to 2147483647 |
| `fs.file-max` | 8192 to 9223372036854775807 |
| `fs.inotify.max_user_watches` | 8192 to 4194304 |
| `fs.inotify.max_user_instances` | 128 to 65536 |
| `net.core.somaxconn` | 128 to 65535 |
| `net.core.netdev_max_backlog` | 1000 to 262144 |
| `net.core.default_qdisc` | `fq`, `fq_codel`, `cake` or `pfifo_fast` |
| `net.ipv4.tcp_congestion_control` | one the kernel lists as available |
| `net.ipv4.tcp_fastopen` | 0 to 3 |
| `net.ipv4.tcp_tw_reuse` | 0 to 2 |
| `net.ipv4.ip_local_port_range` | two ports, low then high |

The values are written live and to `/etc/sysctl.d/99-zz-darkpanel.conf`, which
sorts after `99-sysctl.conf`, so it wins among the `sysctl.d` files at boot.
`tuning.sysctl.get` names any file that sets the same parameter after it, such
as `/etc/ufw/sysctl.conf`, which UFW applies after `systemd-sysctl`.

### Swap file

`/darkpanel.swap` is 256 MiB to 64 GiB and at most the free space on `/` less
1 GiB. Create allocates it with `fallocate`, sets mode 0600, runs `mkswap` and
`swapon`, and adds a marked block to `/etc/fstab`, so it is on again after a
reboot. Resize and remove refuse while the swapped memory exceeds the
available memory less 256 MiB, because turning the file off would have to move
that memory back into RAM; free memory first.

## What it changes on the host

- `/etc/sysctl.d/99-zz-darkpanel.conf`, an owned file, and the live values of
  the parameters it names.
- `/darkpanel.swap`, an owned file, and a marked `swap` block in `/etc/fstab`.

`sudo darkpanel uninstall` turns the swap file off with `swapoff` before it
removes the file and its `/etc/fstab` block, and refuses before any step while
swapped memory exceeds available memory less 256 MiB. It removes
`99-zz-darkpanel.conf`; the kernel parameters then fall back to their boot
values at the next boot, and its plan says so. See [Uninstall
Darkpanel](../uninstall.md).

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `request.invalid_input` | A value is out of range, a parameter is not in the list or another module manages it, a size is outside the free space, or the swapped memory is too large to turn the swap file off. The message says which. | Correct the value, or free memory first. |
| `module.unavailable` | The root file system is not ext4 or xfs, so Darkpanel cannot make a swap file. The message names the file system. | Use the swap the host has, or add a swap partition by hand. |
