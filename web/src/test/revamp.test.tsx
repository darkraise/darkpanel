import { screen } from "@testing-library/react"
import { expect, test } from "vitest"
import type { ComponentType } from "react"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { screenStubs } from "./revamp-stubs" // per-row props and routes, built from each stubsFrom file

type Row = { name: string; load: () => Promise<ComponentType<Record<string, unknown>>>; stubsFrom: string; expects: { role: string; name?: string | RegExp } }

const rows: Row[] = [
  { name: "SummaryPage", load: () => import("@/modules/hostinfo/summary-page").then((m) => m.SummaryPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/hostinfo/summary-page.test.tsx", expects: { role: "img", name: /^Memory: / } },
  { name: "FirewallPage", load: () => import("@/modules/firewall/firewall-page").then((m) => m.FirewallPage as unknown as ComponentType<Record<string, unknown>>), stubsFrom: "web/src/modules/firewall/firewall-page.test.tsx", expects: { role: "img", name: /^Inbound: / } },
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
