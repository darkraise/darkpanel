#!/usr/bin/env bash
# Captures the apt output fixtures the system module's parsers are tested
# against, one directory per supported release:
#   scripts/capture-apt-fixtures.sh <release>
# where <release> is ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12 or
# debian-13. Needs Docker and network access to the distribution mirrors.
#
# Every fixture file holds one or more blocks: a "$ <command>" line, the
# command's combined stdout and stderr, and "exit=<n>".
set -euo pipefail

release=${1:?usage: capture-apt-fixtures.sh <release>}
case "$release" in
  ubuntu-22.04) image=ubuntu@sha256:9abcaffc92a5279fff3a6a41635c0800ea33e528425fb389658806476e7defde ;;
  ubuntu-24.04) image=ubuntu@sha256:73b999393d07f2bf65a40373778a44dc9a6d54ad7718d29247d8ff9413c4d6a8 ;;
  ubuntu-26.04) image=ubuntu@sha256:d66b366dac61b575bee2aef07e9c26e8070096fbe84a3d4d3eb0da069b5d76db ;;
  debian-12) image=debian@sha256:268fb24c39bca92d326dd83ea3abebe4a3f7751c804ffef23bb6d857dd343ff8 ;;
  debian-13) image=debian@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6 ;;
  *) echo "unknown release: $release" >&2; exit 2 ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/internal/modules/system/testdata/$release"
mkdir -p "$out"

docker run --rm -i --platform linux/amd64 -e RELEASE="$release" -v "$out:/out" "$image" bash -s <<'CAPTURE'
set -u
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_SUSPEND=1 NEEDRESTART_MODE=l

plan=(apt-get -s -q -o DPkg::Lock::Timeout=10)
readsim=(apt-get -s -q -o Debug::NoLocking=true)
run=(apt-get -y -q -o Dpkg::Use-Pty=0 -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)

block() {
  local f=/out/$1.txt; shift
  echo "\$ $*" >>"$f"
  "$@" >>"$f" 2>&1
  echo "exit=$?" >>"$f"
}
fresh() { rm -f "/out/$1.txt"; }
quiet() { "$@" >/dev/null 2>&1 || { echo "setup failed: $*" >&2; exit 1; }; }

quiet apt-get -q update

fresh install; block install "${plan[@]}" install -- jq

quiet "${run[@]}" install -- jq
# Installing the oldest listed version of a package that has a newer one
# leaves something to upgrade on every release, whatever the image's age.
old=""
for p in tzdata ca-certificates; do
  v=$(apt-cache madison "$p" | awk -F'|' '{gsub(/ /, "", $2); print $2}' | sort -V | uniq | head -1)
  n=$(apt-cache madison "$p" | awk -F'|' '{gsub(/ /, "", $2); print $2}' | sort -u | wc -l)
  if [ "$n" -ge 2 ]; then old=$p; quiet "${run[@]}" --allow-downgrades install -- "$p=$v"; break; fi
done
[ -n "$old" ] || { echo "no upgradable package on $RELEASE" >&2; exit 1; }

fresh remove; block remove "${plan[@]}" remove -- libjq1
fresh purge; block purge "${plan[@]}" purge -- libjq1
fresh upgrade; block upgrade "${plan[@]}" install --only-upgrade -- "$old"
fresh dist-upgrade; block dist-upgrade "${readsim[@]}" dist-upgrade
fresh dist-upgrade-phased; block dist-upgrade-phased "${readsim[@]}" -o APT::Get::Always-Include-Phased-Updates=true dist-upgrade

dep=$(apt-cache depends --installed libjq1 | awk '/Depends: libonig/ {print $2; exit}')
quiet dpkg --remove --force-depends "$dep"
fresh fix-install; block fix-install "${plan[@]}" -f install
quiet "${run[@]}" -f install

# flock(1) takes a BSD lock that apt's fcntl locks never see, so a real apt
# run, parked in a maintainer script, holds the lock.
mkdir -p /root/holder/DEBIAN
printf 'Package: dp-holder\nVersion: 1.0\nArchitecture: all\nMaintainer: none <none@example.invalid>\nDescription: holds the dpkg lock\n' >/root/holder/DEBIAN/control
printf '#!/bin/sh\n[ -f /root/hold ] && sleep 120\nexit 0\n' >/root/holder/DEBIAN/postinst
chmod 755 /root/holder/DEBIAN/postinst
quiet dpkg-deb --build /root/holder /root/dp-holder.deb
touch /root/hold
apt-get -y -q install /root/dp-holder.deb >/dev/null 2>&1 &
for _ in $(seq 1 120); do
  # cmdline separates the arguments with NUL bytes, which "." matches.
  grep -qsa 'slee[p].120' /proc/[0-9]*/cmdline && break
  sleep 0.5
done
fresh lock-held
block lock-held "${plan[@]}" install -- jq
block lock-held "${readsim[@]}" install -- jq
# A real run is the only one that sees the lock; a short timeout keeps the
# capture fast and prints the same messages.
block lock-held apt-get -y -q -o Dpkg::Use-Pty=0 -o DPkg::Lock::Timeout=3 install -- less

# Killing the parked run leaves dpkg's update journal behind: interrupted.
for p in /proc/[0-9]*; do
  case "$(tr '\0' ' ' <"$p/cmdline" 2>/dev/null)" in
    *apt-get*install*|*dpkg*|*postinst*|sleep\ 120*) kill -9 "${p#/proc/}" 2>/dev/null ;;
  esac
done
sleep 1
rm -f /root/hold
fresh dpkg-interrupted
block dpkg-interrupted "${plan[@]}" install -- jq
block dpkg-interrupted apt-get -y -q -o Dpkg::Use-Pty=0 -o DPkg::Lock::Timeout=10 install -- less
block dpkg-interrupted dpkg --audit
quiet dpkg --configure -a

echo 'deb http://127.0.0.1:9/debian nowhere main' >/etc/apt/sources.list.d/unreachable.list
fresh update-bad-source; block update-bad-source apt-get -q update
rm -f /etc/apt/sources.list.d/unreachable.list
quiet apt-get -q update

quiet "${run[@]}" dist-upgrade
fresh summary-only; block summary-only "${readsim[@]}" dist-upgrade

. /etc/os-release
# A container shares the host's kernel, so uname and /proc/version describe
# the host; the kernel package's version format is what a container can show.
if [ "$ID" = ubuntu ]; then kernel=linux-image-generic; else kernel=linux-image-amd64; fi
fresh proc-version
block proc-version apt-cache policy "$kernel"
block proc-version dpkg-query -W -f '${Package} ${Version}\n' 'linux-image-*'

if [ "$ID" = ubuntu ]; then
  providers=$(apt-cache depends ubuntu-minimal | awk '/Depends: sudo/ {print $2}')
else
  providers=sudo
fi
quiet "${run[@]}" install -- $providers
fresh sudo-provider
block sudo-provider dpkg-query -S /usr/bin/sudo
block sudo-provider readlink -f /usr/bin/sudo
block sudo-provider dpkg-query -S "$(readlink -f /usr/bin/sudo)"

fresh docker-io-policy; block docker-io-policy apt-cache policy docker.io
CAPTURE
echo "wrote $out"
