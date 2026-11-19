import type { AutoUpdates, DockerState, InstalledPackage, RebootState, Updates, Upgradable } from "./types"

// The worst case of the UX-7 matrix: 400 upgradable packages, 70-character
// package names and 3000 installed packages.

const NAME_LENGTH = 70

// longName pads a numbered package name to exactly 70 characters.
function longName(prefix: string, i: number): string {
  const tail = `-${i}`
  return `${prefix}${"x".repeat(NAME_LENGTH - prefix.length - tail.length)}${tail}`
}

const upgradable: Upgradable[] = Array.from({ length: 400 }, (_, i) => ({
  name: longName("lib", i),
  from: `2.${i % 10}.1-1ubuntu0.24.04.1`,
  to: `2.${i % 10}.1-1ubuntu0.24.04.2`,
  origin: i % 8 === 0 ? "noble-security" : "noble-updates",
  security: i % 8 === 0,
}))

export const updatesFixture: Updates = {
  upgradable,
  removals: [longName("linux-modules-extra-", 1)],
  heldBack: [
    { name: "systemd", version: "255.4-1ubuntu8.5", reason: "phased" },
    { name: longName("pinned-", 2), version: "1.0.0-1", reason: "held" },
  ],
  refreshedAt: "2026-11-19T11:40:00Z",
  failedSources: [],
  dpkgInterrupted: false,
}

export const installedFixture: InstalledPackage[] = Array.from({ length: 3000 }, (_, i) => ({
  name: i === 0 ? "sudo" : longName("pkg-", i),
  version: `1.${i % 50}.0-1`,
  arch: i % 5 === 0 ? "all" : "amd64",
  description: `Package number ${i} of the worst-case installed list`,
  protected: i === 0 ? "Removing it would lock you out of administration" : null,
}))

export const rebootFixture: RebootState = {
  required: true,
  packages: ["linux-image-6.8.0-48-generic", "libc6"],
  kernel: { running: "6.8.0-45-generic", newest: "6.8.0-48-generic" },
  services: ["ssh.service", "cron.service", "systemd-logind.service"],
}

export const autoupdatesFixture: AutoUpdates = { installed: true, enabled: true }

export const dockerFixture: DockerState = {
  state: "docker-ce",
  version: "27.3.1",
  updateAvailable: true,
  running: true,
  containers: 12,
  images: 20,
  volumes: 4,
  labelled: [],
}
