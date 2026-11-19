import { render, screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { Gauge } from "./gauge"
import { UsageBar } from "./usage-bar"
import { StatusDonut } from "./status-donut"
import { ResourceCard } from "./resource-card"
import { Stepper } from "./stepper"
import { Server } from "lucide-react"

test("A6: a gauge and a usage bar expose value and limit as text", () => {
  render(<><Gauge value={6.2} max={8} label="Memory" unit="GiB" /><UsageBar used={41} total={100} label="Disk" unit="GiB" /></>)
  expect(screen.getByRole("img", { name: "Memory: 6.2 of 8 GiB (78%)" })).toBeTruthy()
  expect(screen.getByRole("img", { name: "Disk: 41 of 100 GiB (41%)" })).toBeTruthy()
  screen.getByText("41 / 100 GiB")
})

test("A6: a gauge clamps an over-limit value and a zero limit", () => {
  render(<><Gauge value={9} max={8} label="Swap" /><Gauge value={0} max={0} label="Empty" /></>)
  screen.getByRole("img", { name: "Swap: 9 of 8 (100%)" })
  screen.getByRole("img", { name: "Empty: 0 of 0 (0%)" })
})

test("A6: a donut names every segment with its count", () => {
  render(<StatusDonut label="Containers" segments={[{ status: "ok", label: "Running", value: 5 }, { status: "off", label: "Stopped", value: 2 }]} />)
  screen.getByRole("img", { name: "Containers: Running 5, Stopped 2" })
})

test("A6: a resource card shows title, status and its actions", () => {
  render(<ResourceCard icon={Server} title="web-1" status={{ status: "ok", label: "Online" }} actions={<button type="button">Open</button>} />)
  const card = within(screen.getByRole("article", { name: "web-1" }))
  card.getByText("Online")
  card.getByRole("button", { name: "Open" })
})

test("A6: the stepper marks the current and failed step", () => {
  render(<Stepper steps={[{ id: "plan", label: "Plan" }, { id: "confirm", label: "Confirm" }, { id: "apply", label: "Apply" }]} current="apply" failed />)
  const list = screen.getByRole("list", { name: "Progress" })
  expect(within(list).getByText("Apply").closest("li")?.getAttribute("aria-current")).toBe("step")
  expect(within(list).getByText("Apply").closest("li")?.dataset.status).toBe("error")
})

test("A6: graphics pass axe in light and dark", async () => {
  for (const mode of ["light", "dark"]) {
    document.documentElement.className = mode
    const { container, unmount } = render(<><Gauge value={1} max={4} label="Load" /><UsageBar used={1} total={2} label="Swap" /></>)
    await expectNoAxeViolations(container)
    unmount()
  }
})
