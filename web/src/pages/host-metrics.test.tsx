import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import type { MetricsHistory, MetricsPoint, ModuleState, PollStatus } from "@/api/types"
import { formatTime } from "@/lib/format"
import { charts, summary } from "@/lib/metrics"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { MetricsSection } from "./host-metrics"

const HISTORY = "POST /api/v1/actions/metrics.history.get/v1/read"
const SUMMARY = "GET /api/v1/hosts/h1/resources/hostinfo/summary"
const GIB = 1024 ** 3

const metricsOn: ModuleState = { id: "metrics", version: 1, enabled: true, available: true, reason: null, checkedAt: null }
const metricsOff: ModuleState = { ...metricsOn, enabled: false }

function point(minute: number, over: Partial<MetricsPoint> = {}): MetricsPoint {
  return {
    ts: new Date(Date.UTC(2026, 10, 24, 9, minute)).toISOString(), cpu: 10 + minute, memUsed: 4 * GIB, memTotal: 8 * GIB, swapUsed: 0, swapTotal: 0,
    load1: 2 + minute / 10, load5: 1, load15: 1, fs: [{ mount: "/", used: 140, total: 200 }], net: [{ iface: "eth0", rxBps: 2_000_000, txBps: 1_000_000 }], ...over,
  }
}

const okStatus: PollStatus = { lastOk: "2026-11-24T09:03:00Z", lastErrorCode: null, lastErrorAt: null, clockSkewS: 0 }

function history(points: MetricsPoint[], status: PollStatus | null = okStatus, over: Partial<MetricsHistory> = {}): MetricsHistory {
  return { hostId: "h1", range: "1h", resolution: "1m", points, status, ...over }
}

function render(h: MetricsHistory | null, hostOver: Parameters<typeof makeHost>[0] = {}, hostScope: "admin" | "read" = "admin") {
  const calls = stubFetch({
    [HISTORY]: { body: { output: h } },
    [SUMMARY]: { body: { cpu: { logicalCount: 4 } } },
  })
  renderWithProviders(<MetricsSection host={makeHost({ id: "h1", name: "web-1", online: true, modules: [metricsOn], ...hostOver })} />, { hostScope })
  return calls
}

describe("states", () => {
  test("UX-7 no data yet says the first samples are coming", async () => {
    render(history([], null))
    expect(await screen.findByText("Collecting — the first samples appear within 2 minutes")).toBeTruthy()
    expect(screen.queryByRole("img")).toBeNull()
  })

  test("UX-7 offline shows Offline since <time> above the kept history", async () => {
    render(history([point(0), point(1)]), { online: false, lastSeenAt: "2026-11-24T09:05:00Z" })
    const banner = await screen.findByText(/^Offline since /)
    expect(banner.textContent).not.toContain("—")
    const chart = await screen.findByRole("img", { name: /^CPU \(%\)/ })
    expect(banner.compareDocumentPosition(chart) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getAllByRole("img")).toHaveLength(5)
  })

  test("UX-7 a disabled module says so, with Enable for admins only", async () => {
    const calls = render(null, { modules: [metricsOff] })
    expect(await screen.findByText("Metrics are off on this host")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Enable" }))
    expect(await screen.findByRole("dialog", { name: "Enable metrics on web-1" })).toBeTruthy()
    await waitFor(() => expect(calls.find((c) => c.path.endsWith("core.module.enable/v1/plan"))?.body).toEqual({ input: { hostId: "h1", moduleId: "metrics" } }))
    expect(calls.some((c) => c.path.includes("metrics.history.get"))).toBe(false)
  })

  test("a reader sees the disabled module without Enable", async () => {
    render(null, { modules: [metricsOff] }, "read")
    expect(await screen.findByText("Metrics are off on this host")).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Enable" })).toBeNull()
  })

  test("UX-7 a failing poll shows No new samples since <time>: <reason>", async () => {
    render(history([point(0)], { lastOk: "2026-11-24T09:00:00Z", lastErrorCode: "metrics.clock_skew", lastErrorAt: "2026-11-24T09:10:00Z", clockSkewS: -180 }))
    const line = await screen.findByText(/^No new samples since /)
    expect(line.textContent).toBe(`No new samples since ${formatTime("2026-11-24T09:00:00Z")}: the host's clock is 180 s behind`)
  })

  test("a poll failing before any sample says why", async () => {
    render(history([], { lastOk: null, lastErrorCode: "core.deadline_exceeded", lastErrorAt: "2026-11-24T09:10:00Z", clockSkewS: null }))
    expect(await screen.findByText("No samples yet: the host did not answer")).toBeTruthy()
    expect(screen.queryByText(/^Collecting/)).toBeNull()
  })

  test("A40 a missing minute is a null point, a break, never zero", () => {
    const c = charts(history([point(0), point(1), point(4)]), 4)
    expect(c.cpu.times).toHaveLength(5)
    expect(c.cpu.series[0]!.values).toEqual([10, 11, null, null, 14])
    expect(c.disk.series[0]!.values[2]).toBeNull()
    expect(c.network.series.map((s) => s.values[3])).toEqual([null, null])
    expect(summary(c.cpu.series[0]!.values, " %")).toBe("latest 14.0 %, min 10.0 %, max 14.0 %")
  })

  test("hour points leave a missing hour as a break", () => {
    const at = (h: number) => point(0, { ts: new Date(Date.UTC(2026, 10, 24, h)).toISOString() })
    const c = charts(history([at(1), at(3)], okStatus, { resolution: "1h" }), null)
    expect(c.cpu.series[0]!.values).toEqual([10, null, 10])
  })
})

describe("range and summaries", () => {
  test("the range is a radio group moved by arrow keys, and reads that range", async () => {
    const calls = render(history([point(0), point(1)]))
    const group = await screen.findByRole("radiogroup", { name: "Range" })
    expect(within(group).getAllByRole("radio").map((r) => r.textContent)).toEqual(["1h", "24h", "7d", "30d", "90d"])
    const user = userEvent.setup()
    await user.click(within(group).getByRole("radio", { name: "1h" }))
    await user.keyboard("{ArrowRight}")
    await waitFor(() => expect(within(group).getByRole("radio", { name: "24h" }).getAttribute("aria-checked")).toBe("true"))
    await waitFor(() => expect(calls.some((c) => (c.body as { input?: { range?: string } } | undefined)?.input?.range === "24h")).toBe(true))
    expect(await screen.findByRole("img", { name: /^CPU \(%\), last 24 hours\./ })).toBeTruthy()
  })

  test("each chart's accessible name is its text summary", async () => {
    render(history([point(0), point(1), point(2)]))
    expect(await screen.findByRole("img", { name: "CPU (%), last hour. CPU: latest 12.0 %, min 10.0 %, max 12.0 %." })).toBeTruthy()
    expect(screen.getByRole("img", { name: "Memory (GiB), last hour. Used: latest 4.0 GiB, min 4.0 GiB, max 4.0 GiB." })).toBeTruthy()
    expect(screen.getByRole("img", { name: "Disk per mount (%), last hour. /: latest 70.0 %, min 70.0 %, max 70.0 %." })).toBeTruthy()
    expect(screen.getByRole("img", { name: "Network per interface (MB/s), last hour. eth0 in: latest 2.0 MB/s, min 2.0 MB/s, max 2.0 MB/s; eth0 out: latest 1.0 MB/s, min 1.0 MB/s, max 1.0 MB/s." })).toBeTruthy()
    expect(await screen.findByRole("img", { name: "Load per CPU, last hour. 1-minute load: latest 0.6, min 0.5, max 0.6." })).toBeTruthy()
  })

  test("16 mounts draw the 8 fullest and say so", () => {
    const fs = Array.from({ length: 16 }, (_, i) => ({ mount: `/m${String(i).padStart(2, "0")}`, used: i, total: 100 }))
    const c = charts(history([point(0, { fs })]), null)
    expect(c.disk.series.map((s) => s.label)).toEqual(["/m15", "/m14", "/m13", "/m12", "/m11", "/m10", "/m09", "/m08"])
    expect(c.disk.hidden).toEqual({ shown: 8, of: 16, noun: "mounts" })
    expect(c.perCpu).toBe(false)
  })
})
