# ADR 0008: Integration test harness

**Requirements:** R21, R49, R75

## Status

Proposed

## Context

Success measures S1, S2, S3 and S5 need real UFW, Docker and tailscaled on
four operating systems. The owner chose QEMU/KVM on GitHub Actions on
2026-10-17. The development host is an LXC container without `/dev/kvm`.

Review finding 26 raised three problems with the first proposal. Nested
virtualization on GitHub runners is not officially supported. A scanner placed
inside a private range would be exempted by ufw-docker rules and so could not
detect exposure. IPv6 and fault injection were missing.

## Decision

- QEMU/KVM VMs on GitHub-hosted Linux runners from pinned, checksummed
  official cloud images (Ubuntu 22.04 and 24.04, Debian 12 and 13) with
  cloud-init, one operating system per matrix job. A first step checks for
  `/dev/kvm` and fails loudly if it is missing; GitHub documents nested
  virtualization as not officially supported, so this is a known risk with
  self-hosted KVM runners as the fallback.
- A scanner VM sits on an "internet" network addressed outside private ranges
  (198.18.0.0/15 and an IPv6 documentation prefix), so ufw-docker private-range
  exemptions cannot mask exposure; IPv4 and IPv6 are both tested.
- Fault injection: kill the agent mid-apply, reboot during an unconfirmed
  change, and ship a deliberately broken binary to the updater.
- Tailscale tests use a dedicated test tailnet with ephemeral auth keys stored
  as CI secrets (needed from M5).
- arm64 is covered by builds and unit tests in v1; arm64 VM tests are Unknown
  until GitHub arm64 runners are confirmed to expose KVM.
- Local runs need a KVM-capable machine; the development host is an LXC
  container that could get one if Proxmox passes `/dev/kvm` through.

## Alternatives considered

- Proxmox VMs in the owner's homelab: rejected as the primary backend; CI and
  contributors could not use it.
- Privileged Docker containers with systemd: rejected; they share the host
  kernel and cannot prove per-distribution firewall behaviour.
- Scanner on a private network: rejected; see Context.

## Consequences

- The harness epic builds image pinning, VM boot, the scanner network and
  fault injection.
- A missing `/dev/kvm` fails the job instead of skipping tests.
- M5 needs a test tailnet and CI secrets.
- arm64 VM coverage stays Unknown until confirmed.
