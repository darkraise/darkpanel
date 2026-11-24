import { screen } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { TailnetDevicesPage } from "./tailnet-devices"

const LIST = "POST /api/v1/actions/core.tailnetdevice.list/v1/read"
const THEMES = ["light", "dark"] as const
const devices = [
  { stableId: "nL", nodeName: "laptop.tail1234.ts.net", rememberedAt: "2026-10-02T09:00:00Z", lastUsedAt: "2026-11-02T09:30:00Z" },
  { stableId: "nE", nodeName: "", rememberedAt: "2026-10-01T09:00:00Z", lastUsedAt: "2026-11-01T09:30:00Z" },
]
const states: { name: string; answer: StubResponse; ready: string }[] = [
  { name: "the list", answer: { body: { output: { devices } } }, ready: "Unnamed device" },
  { name: "the empty state", answer: { body: { output: { devices: [] } } }, ready: "No remembered devices" },
  {
    name: "the error state",
    answer: { status: 500, body: { type: "about:blank", title: "internal", status: 500, detail: "server detail for internal", code: "internal" } },
    ready: "server detail for internal",
  },
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
})

test.each(states.flatMap((s) => THEMES.map((theme) => ({ ...s, theme }))))("A23 $name in $theme has no axe violation", async ({ answer, ready, theme }) => {
  document.documentElement.classList.toggle("dark", theme === "dark")
  stubFetch({ [LIST]: answer })
  const { container } = renderWithProviders(<TailnetDevicesPage />)
  expect(await screen.findByText(ready)).toBeTruthy()
  await expectNoAxeViolations(container)
})
