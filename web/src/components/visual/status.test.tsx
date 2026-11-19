import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { StatusDot } from "./status-dot"
import { StatusPill } from "./status-pill"
import { StatTile } from "./stat-tile"
import { InfoTip } from "./info-tip"
import { SectionHeader } from "./section-header"
import { ModuleIcon } from "./module-icon"
import { Server } from "lucide-react"

test("A6: a status is an icon and a label, never colour alone", () => {
  render(<><StatusDot status="error" label="Offline" /><StatusPill status="ok" label="Running" /></>)
  expect(screen.getByRole("img", { name: "Offline" })).toBeTruthy()
  expect(screen.getByText("Running")).toBeTruthy()
  expect(document.querySelector("[data-status=ok] svg")).not.toBeNull()
})

test("spec 2 Accessibility: a live status pill announces changes through a live region", () => {
  render(<StatusPill status="info" label="Rebooting" live />)
  expect(screen.getByRole("status").textContent).toContain("Rebooting")
})

test("A6: a stat tile exposes its figure and caption", () => {
  render(<StatTile icon={Server} figure="12" caption="Upgradable" />)
  expect(screen.getByRole("group", { name: "Upgradable: 12" })).toBeTruthy()
})

test("A6: an info tip opens on focus and names its content", async () => {
  render(<InfoTip label="Why held">Held by apt-mark</InfoTip>)
  await userEvent.tab()
  expect(await screen.findByText("Held by apt-mark")).toBeTruthy()
})

test("A6: an info tip toggles on a tap, for touch screens", async () => {
  render(<InfoTip label="Why held">Held by apt-mark</InfoTip>)
  await userEvent.click(screen.getByRole("button", { name: "Why held" }))
  expect(await screen.findByText("Held by apt-mark")).toBeTruthy()
})

test("A6: the section header renders its title as a heading, and every module has an icon", () => {
  render(<><SectionHeader title="System" status={{ status: "warn", label: "Reboot needed" }} />{["hostinfo", "firewall", "docker", "tailscale", "tailnet", "derp", "npm", "cloudflare", "panel", "system"].map((m) => <ModuleIcon key={m} module={m} />)}</>)
  expect(screen.getByRole("heading", { name: "System" })).toBeTruthy()
  expect(document.querySelectorAll("[data-module-icon] svg")).toHaveLength(10)
})

test("A6: no axe violation in light and dark", async () => {
  for (const mode of ["light", "dark"]) {
    document.documentElement.className = mode
    const { container, unmount } = render(<><StatusPill status="warn" label="Pending" /><StatTile icon={Server} figure="3" caption="Hosts" /></>)
    await expectNoAxeViolations(container)
    unmount()
  }
})
