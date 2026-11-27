// The M11 fixture data: what the services, users, fail2ban, console, jobs and
// tuning reads answer, in their API shapes, for the state boards, the unit
// tests and the e2e mock API. Each worst case holds the longest lists and
// names its UI item must hold (spec UX-2, UX-9 and the boards' worst cases).

const NOW = "2026-11-26T09:40:00Z"
const LONG = (prefix: string, length: number) => (prefix + "-segment".repeat(Math.ceil(length / 8))).slice(0, length)
// UNBROKEN is a text of length with no point a browser may break it at.
const UNBROKEN = (prefix: string, length: number) => (prefix + "x".repeat(length)).slice(0, length)

export interface M11Unit {
  name: string
  type: string
  description: string
  load: string
  active: string
  sub: string
  enabled: string
  mainPid?: number
}

function unit(name: string, description: string, active: string, sub: string, enabled = "enabled", mainPid?: number): M11Unit {
  return { name, type: name.slice(name.lastIndexOf(".") + 1), description, load: "loaded", active, sub, enabled, ...(mainPid === undefined ? {} : { mainPid }) }
}

export const unitsDefault: M11Unit[] = [
  unit("cron.service", "Regular background program processing daemon", "active", "running", "enabled", 612),
  unit("darkpanel-agent.service", "Darkpanel agent", "active", "running", "enabled", 701),
  unit("docker.service", "Docker Application Container Engine", "active", "running", "enabled", 845),
  unit("fail2ban.service", "Fail2Ban Service", "failed", "failed"),
  unit("nginx.service", "A high performance web server and a reverse proxy server", "active", "running", "enabled", 990),
  unit("postgresql@16-main.service", "PostgreSQL Cluster 16-main", "failed", "failed"),
  unit("ssh.service", "OpenBSD Secure Shell server", "active", "running", "enabled", 640),
  unit("systemd-networkd.service", "Network Configuration", "active", "running", "enabled", 402),
  unit("tailscaled.service", "Tailscale node agent", "active", "running", "enabled", 655),
  unit("apt-daily.timer", "Daily apt download activities", "active", "waiting"),
  unit("logrotate.service", "Rotate log files", "inactive", "dead", "static"),
  unit("certbot.service", "Certbot", "active", "exited", "static"),
]

// unitsWorst is 400 units, a tenth of them failed, with 120-character names.
export const unitsWorst: M11Unit[] = Array.from({ length: 400 }, (_, i) => {
  const failed = i % 10 === 3
  return unit(`${LONG(`worker-${String(i).padStart(3, "0")}`, 112)}.service`, `Queue worker ${i} for the payments and billing pipeline`, failed ? "failed" : "active", failed ? "failed" : "running")
})

// Units Darkpanel refuses to drive or asks the name for (services.protected).
export const darkpanelUnits = ["darkpanel-agent.service", "darkpanel-controller.service"]
export const protectedUnits = ["ssh.service", "systemd-networkd.service", "tailscaled.service"]

export interface M11JournalEntry {
  time: number // microseconds since the epoch
  priority: number | null
  unit: string
  pid: number
  message: string
  truncated: boolean
}

const PRIORITY_CYCLE = [6, 6, 4, 6, 3, 6, 5, 2]
const T0 = Date.parse(NOW) * 1000

export function journalEntries(count: number, unitName = "nginx.service"): M11JournalEntry[] {
  return Array.from({ length: count }, (_, i) => {
    const truncated = i % 97 === 5
    return {
      time: T0 - (count - i) * 1_500_000,
      priority: PRIORITY_CYCLE[i % PRIORITY_CYCLE.length] ?? 6,
      unit: unitName,
      pid: 990,
      message: truncated ? `upstream response ${"x".repeat(180)}` : `172.20.0.${i % 250} - - [26/Nov/2026] "GET /api/v1/items/${i} HTTP/1.1" 200 ${512 + i}`,
      truncated,
    }
  })
}

export const journalDefault = { entries: journalEntries(30), more: false }
export const journalWorst = { entries: journalEntries(2000), more: true, cursor: "s=fixture;i=2000" }

export interface M11Account {
  name: string
  uid: number
  home: string
  shell: string
  groups: string[]
  sudo: boolean
  locked: boolean
  passwordSet: boolean
  keys: number
}

export const accountsDefault: M11Account[] = [
  { name: "root", uid: 0, home: "/root", shell: "/bin/bash", groups: ["root"], sudo: true, locked: false, passwordSet: false, keys: 1 },
  { name: "deploy", uid: 1000, home: "/home/deploy", shell: "/bin/bash", groups: ["deploy", "sudo", "docker"], sudo: true, locked: false, passwordSet: true, keys: 2 },
  { name: "alice", uid: 1001, home: "/home/alice", shell: "/bin/bash", groups: ["alice"], sudo: false, locked: true, passwordSet: true, keys: 0 },
  { name: "backup", uid: 1002, home: "/home/backup", shell: "/usr/sbin/nologin", groups: ["backup"], sudo: false, locked: false, passwordSet: false, keys: 1 },
]

// accountsWorst is 200 accounts with 32-character names.
export const accountsWorst: M11Account[] = [
  ...accountsDefault,
  ...Array.from({ length: 196 }, (_, i) => ({
    name: LONG(`svc${String(i).padStart(3, "0")}`, 32),
    uid: 2000 + i,
    home: `/home/${LONG(`svc${String(i).padStart(3, "0")}`, 32)}`,
    shell: "/bin/bash",
    groups: ["users"],
    sudo: i % 7 === 0,
    locked: i % 11 === 0,
    passwordSet: i % 2 === 0,
    keys: i % 4,
  })),
]

export const keysDeploy = {
  keys: [
    { type: "ssh-ed25519", bits: 256, fingerprint: "SHA256:9T2mQ0fZr8w1kV3yJ5cXb7nLp4aHs6dGe2uQiOtMvYk", comment: "deploy@laptop", options: "" },
    { type: "ssh-rsa", bits: 4096, fingerprint: "SHA256:Kq1vR7sT3uW9xY2zA4bC6dE8fG0hI5jK7lM9nO1pQ3r", comment: LONG("ci-runner-production-eu-west", 64), options: 'from="10.0.0.0/8"' },
  ],
}

const sshdEffective = {
  passwordAuthentication: true,
  kbdInteractiveAuthentication: false,
  permitRootLogin: "prohibit-password",
  maxAuthTries: 6,
  loginGraceTime: 120,
  allowUsers: [] as string[],
  allowGroups: [] as string[],
  clientAliveInterval: 0,
  clientAliveCountMax: 3,
  x11Forwarding: false,
}

export const sshdState = { effective: sshdEffective, set: {}, readOnly: [] as string[], listsSetBy: "", matchIn: "" }

export const fail2banNotInstalled = { installed: false, version: "", service: "", jails: [], crowdsec: false, settings: null, ignoreFixed: [] as string[] }

const IGNORE_FIXED = ["127.0.0.1/8", "::1", "100.64.0.0/10", "fd7a:115c:a1e0::/48", "203.0.113.10"]

export const fail2banInstalled = {
  installed: true,
  version: "1.0.2",
  service: "active",
  jails: [
    { name: "sshd", banned: 3, failed: 2, failedTotal: 418 },
    { name: "recidive", banned: 1, failed: 0, failedTotal: 9 },
  ],
  crowdsec: false,
  settings: { bantimeMinutes: 10, findtimeMinutes: 10, maxRetry: 5, ignoreIp: [...IGNORE_FIXED, "198.51.100.0/24"], recidive: true },
  ignoreFixed: IGNORE_FIXED,
}

export const fail2banCrowdsec = { ...fail2banInstalled, crowdsec: true }

export interface M11Ban {
  jail: string
  address: string
  start: string
  end: string | null
}

export const bansDefault: M11Ban[] = [
  { jail: "sshd", address: "192.0.2.44", start: "2026-11-26T09:31:02Z", end: "2026-11-26T09:41:02Z" },
  { jail: "sshd", address: "198.51.100.201", start: "2026-11-26T09:35:40Z", end: "2026-11-26T09:45:40Z" },
  { jail: "sshd", address: "2001:db8:85a3::8a2e:370:7334", start: "2026-11-26T09:38:12Z", end: "2026-11-26T09:48:12Z" },
  { jail: "recidive", address: "192.0.2.44", start: "2026-11-25T22:10:00Z", end: "2026-12-02T22:10:00Z" },
]

// bansWorst is 500 bans, a fifth of them IPv6 at full length.
export const bansWorst: M11Ban[] = Array.from({ length: 500 }, (_, i) => ({
  jail: i % 9 === 0 ? "recidive" : "sshd",
  address: i % 5 === 0 ? `2001:0db8:85a3:0000:0000:8a2e:0370:${(0x7000 + i).toString(16)}` : `203.0.113.${i % 250}`,
  start: "2026-11-26T09:00:00Z",
  end: i % 9 === 0 ? null : "2026-11-26T10:00:00Z",
}))

export interface M11FileInfo {
  name: string
  escaped?: boolean
  raw?: string
  type: "file" | "dir" | "symlink" | "other"
  size: number
  mode: string
  uid: number
  gid: number
  owner: string
  group: string
  modified: string
  target?: string
  protected?: string
}

function file(name: string, type: M11FileInfo["type"], size: number, mode: string, over: Partial<M11FileInfo> = {}): M11FileInfo {
  return { name, type, size, mode, uid: 0, gid: 0, owner: "root", group: "root", modified: "2026-11-25T18:02:11Z", ...over }
}

export const filesDefault = {
  path: "/etc",
  entries: [
    file("nginx", "dir", 4096, "0755"),
    file("ssh", "dir", 4096, "0755"),
    file("darkpanel", "dir", 4096, "0750", { protected: "Darkpanel's own files are read-only here" }),
    file("hosts", "file", 221, "0644"),
    file("fstab", "file", 664, "0644"),
    file("localtime", "symlink", 33, "0777", { target: "/usr/share/zoneinfo/Etc/UTC" }),
    file("shadow", "file", 1290, "0640", { group: "shadow", gid: 42 }),
  ],
}

// filesWorst is a 2000-entry page with a next cursor, 255-byte names (the
// second one with no break point) and names that are not UTF-8, escaped.
export const filesWorst = {
  path: "/srv/uploads",
  next: "cursor-2000",
  entries: Array.from({ length: 2000 }, (_, i) =>
    i % 400 === 7
      ? file(`report-\\xff\\xfe-${i}.csv`, "file", 2048 + i, "0644", { escaped: true, raw: "cmVwb3J0LS8tMC5jc3Y=", owner: "deploy", group: "deploy", uid: 1000, gid: 1000 })
      : file(i === 1 ? UNBROKEN("upload1", 255) : i % 100 === 1 ? LONG(`upload-${i}`, 255) : `upload-${String(i).padStart(4, "0")}.bin`, "file", 1_048_576 * (i % 50), "0644", { owner: "deploy", group: "deploy", uid: 1000, gid: 1000 }),
  ),
}

export interface M11Job {
  slug: string
  description: string
  schedule: string
  user: string
  command: string
  timeoutSeconds: number
  catchUp: boolean
  randomDelaySeconds: number
  enabled: boolean
  running: boolean
  nextRun: string
  lastStart: string
  lastResult: string
  exitStatus: number | null
}

export const jobsDefault = {
  jobs: [
    { slug: "db-backup", description: "Nightly database backup", schedule: "*-*-* 03:00:00", user: "backup", command: "/usr/local/bin/pg-backup --all", timeoutSeconds: 3600, catchUp: true, randomDelaySeconds: 300, enabled: true, running: false, nextRun: "2026-11-27T03:00:00Z", lastStart: "2026-11-26T03:00:04Z", lastResult: "success", exitStatus: 0 },
    { slug: "cache-warm", description: "Warm the page cache", schedule: "hourly", user: "deploy", command: "curl -fsS http://127.0.0.1/warm", timeoutSeconds: 120, catchUp: false, randomDelaySeconds: 0, enabled: true, running: true, nextRun: "2026-11-26T10:00:00Z", lastStart: "2026-11-26T09:00:00Z", lastResult: "", exitStatus: null },
    { slug: "report-mail", description: "Weekly report", schedule: "Mon *-*-* 07:30:00", user: "deploy", command: "/srv/app/bin/report --mail ops@example.com", timeoutSeconds: 600, catchUp: false, randomDelaySeconds: 0, enabled: false, running: false, nextRun: "", lastStart: "2026-11-23T07:30:01Z", lastResult: "exit-code", exitStatus: 2 },
  ] as M11Job[],
  timers: [
    { name: "apt-daily.timer", unit: "apt-daily.service", source: "/usr/lib/systemd/system/apt-daily.timer", nextRun: "2026-11-26T18:12:00Z", lastRun: "2026-11-26T06:12:00Z" },
    { name: "logrotate.timer", unit: "logrotate.service", source: "/usr/lib/systemd/system/logrotate.timer", nextRun: "2026-11-27T00:00:00Z", lastRun: "2026-11-26T00:00:00Z" },
  ],
  cron: [{ source: "/etc/cron.d/certbot", schedule: "0 */12 * * *", user: "root", command: "test -x /usr/bin/certbot && perl -e 'sleep int(rand(43200))' && certbot -q renew" }],
}

export const jobsEmpty = { jobs: [] as M11Job[], timers: jobsDefault.timers, cron: [] }

// jobsWorst is 100 jobs with 40-character slugs (the first with no break
// point) and long commands, a timer of 200 characters and a cron command of
// 255, both with no break point.
export const jobsWorst = {
  ...jobsDefault,
  timers: [
    ...jobsDefault.timers,
    { name: `${UNBROKEN("worker", 194)}.timer`, unit: "worker.service", source: `/etc/systemd/system/${UNBROKEN("worker", 194)}.timer`, nextRun: "", lastRun: "" },
  ],
  cron: [...jobsDefault.cron, { source: "/etc/cron.d/export", schedule: "*/5 * * * *", user: "deploy", command: UNBROKEN("/srv/export", 255) }],
  jobs: Array.from({ length: 100 }, (_, i) => ({
    ...jobsDefault.jobs[0],
    slug: i === 0 ? UNBROKEN("job000", 40) : LONG(`job-${String(i).padStart(3, "0")}`, 40),
    command: `/srv/app/bin/task --tenant ${i} ${"--flag ".repeat(30)}`,
    lastResult: i % 6 === 2 ? "exit-code" : i % 6 === 4 ? "timeout" : "success",
    exitStatus: i % 6 === 2 ? 1 : i % 6 === 4 ? null : 0,
  })) as M11Job[],
}

export const scheduleNext = { next: ["2026-11-27T03:00:00Z", "2026-11-28T03:00:00Z", "2026-11-29T03:00:00Z"] }

const GIB = 1024 * 1024 * 1024

export const swapDefault = {
  swaps: [
    { path: "/dev/sda3", type: "partition", size: 2 * GIB, used: 310 * 1024 * 1024, darkpanel: false },
    { path: "/darkpanel.swap", type: "file", size: GIB, used: 0, darkpanel: true },
  ],
  memoryTotal: 4 * GIB,
  memoryAvailable: 1.6 * GIB,
  filesystem: "ext4",
  free: 38 * GIB,
  unavailable: "",
}

export const swapUnsupported = { ...swapDefault, swaps: [swapDefault.swaps[0]], filesystem: "btrfs", unavailable: "The root filesystem is btrfs, which cannot hold a swap file here." }

export const sysctlDefault = {
  file: "/etc/sysctl.d/99-zz-darkpanel.conf",
  keys: [
    { key: "vm.swappiness", help: "How readily the kernel moves memory to swap, 0 to 200.", kind: "integer", min: "0", max: "200", choices: [], live: "60", set: "10", laterFiles: [], owner: "", unavailable: "" },
    { key: "net.core.somaxconn", help: "The longest queue of connections waiting to be accepted.", kind: "integer", min: "128", max: "65535", choices: [], live: "4096", set: "", laterFiles: ["/etc/sysctl.d/99-zzz-local.conf"], owner: "", unavailable: "" },
    { key: "net.ipv4.ip_forward", help: "", kind: "integer", min: "", max: "", choices: [], live: "1", set: "", laterFiles: [], owner: "docker", unavailable: "" },
    { key: "net.ipv4.tcp_congestion_control", help: "The TCP congestion control algorithm.", kind: "choice", min: "", max: "", choices: ["bbr", "cubic", "reno"], live: "cubic", set: "", laterFiles: [], owner: "", unavailable: "" },
    { key: "net.ipv4.ip_local_port_range", help: "The ports outgoing connections use.", kind: "port-range", min: "1024", max: "65535", choices: [], live: "32768 60999", set: "", laterFiles: [], owner: "", unavailable: "" },
    { key: "fs.inotify.max_user_watches", help: "Files one user can watch for changes.", kind: "integer", min: "8192", max: "4194304", choices: [], live: "", set: "", laterFiles: [], owner: "", unavailable: "Not in this kernel" },
  ],
}

export const m11Now = NOW
