import { screen } from "@testing-library/react"
import { expect, test } from "vitest"
import type { ComponentType } from "react"
import { SessionProvider } from "@/session/session"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { screenStubs } from "./revamp-stubs" // per-row props and routes, built from each stubsFrom file

type Row = { name: string; load: () => Promise<ComponentType<Record<string, unknown>>>; stubsFrom: string; expects: { role: string; name?: string | RegExp } }

const rows: Row[] = [
  { name: "SummaryPage", load: () => import("@/modules/hostinfo/summary-page").then((m) => m.SummaryPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/hostinfo/summary-page.test.tsx", expects: { role: "img", name: /^Memory: / } },
  { name: "FirewallPage", load: () => import("@/modules/firewall/firewall-page").then((m) => m.FirewallPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/firewall/firewall-page.test.tsx", expects: { role: "img", name: /^Inbound: / } },
  { name: "DockerPage", load: () => import("@/modules/docker/docker-page").then((m) => m.DockerPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/docker/docker-page.test.tsx", expects: { role: "img", name: /^Containers: / } },
  { name: "TailscalePage", load: () => import("@/modules/tailscale/tailscale-page").then((m) => m.TailscalePage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/tailscale/tailscale-page.test.tsx", expects: { role: "status", name: /^Node state: / } },
  { name: "DevicesPage", load: () => import("@/modules/tailnet/devices-page").then((m) => m.DevicesPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/tailnet/devices-page.test.tsx", expects: { role: "img", name: /^(Authorized|Pending approval)$/ } },
  { name: "DerpPage", load: () => import("@/modules/derp/derp-page").then((m) => m.DerpPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/derp/derp-page.test.tsx", expects: { role: "article" } },
  { name: "NpmPage", load: () => import("@/modules/npm/npm-page").then((m) => m.NpmPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/npm/npm-page.test.tsx", expects: { role: "article" } },
  { name: "ConnectorsPage", load: () => import("@/modules/cloudflare/connectors-page").then((m) => m.ConnectorsPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/cloudflare/connectors-page.test.tsx", expects: { role: "img", name: /^(Healthy|Degraded|Down|Inactive)/ } },
  { name: "PanelPage", load: () => import("@/modules/panel/panel-page").then((m) => m.PanelPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/panel/panel-page.test.tsx", expects: { role: "group", name: /: / } },
  { name: "AuditPage", load: () => import("@/pages/audit").then((m) => m.AuditPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/pages/audit.test.tsx", expects: { role: "list", name: "Timeline" } },
  { name: "RolloutsPage", load: () => import("@/pages/rollouts").then((m) => m.RolloutsPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/pages/rollouts.test.tsx", expects: { role: "img", name: /^Outcomes: / } },
  { name: "ChangePage", load: () => import("@/pages/change").then((m) => m.ChangePage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/pages/change.test.tsx", expects: { role: "list", name: "Progress" } },
  { name: "TokensPage", load: () => import("@/pages/tokens").then((m) => m.TokensPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/pages/tokens.test.tsx", expects: { role: "img", name: /^Scope: / } },
  // LoginPage reads the session, so it renders under the provider the app puts above it.
  { name: "LoginPage", load: () => import("@/pages/login").then((m) => () => <SessionProvider><m.LoginPage /></SessionProvider>), stubsFrom: "web/src/pages/login.test.tsx", expects: { role: "img", name: "Darkpanel" } },
]

test.each(rows)("A10, UI-7: $name shows its state as graphics, not key/value text", async (row) => {
  const Screen = await row.load()
  const { path, props, routes } = screenStubs(row.name)
  stubFetch(routes)
  const { container } = renderWithProviders(<Screen {...props} />, { path })
  expect((await screen.findAllByRole(row.expects.role as never, row.expects.name ? { name: row.expects.name } : undefined)).length).toBeGreaterThan(0)
  expect(container.querySelectorAll("dl dt").length).toBe(0)
  for (const img of container.querySelectorAll("[role=img]")) expect(img.getAttribute("aria-label")).toBeTruthy()
})
