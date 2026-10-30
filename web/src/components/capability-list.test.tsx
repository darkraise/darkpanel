import { render, screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { CapabilityList, type Capability } from "./capability-list"

function clRows(): HTMLElement[] {
  return within(screen.getByRole("list", { name: "Capabilities" })).getAllByRole("listitem")
}

const clCaps: Capability[] = [
  { name: "devices:read", state: "ok" },
  { name: "policy:read", state: "missing", detail: "cloud.permission_missing" },
  { name: "auth_keys:write", state: "unverified" },
]

test("each capability shows its name and its state", () => {
  render(<CapabilityList capabilities={clCaps} />)
  const rows = clRows()
  expect(rows).toHaveLength(3)
  expect(within(rows[0]!).getByText("devices:read")).toBeTruthy()
  expect(within(rows[0]!).getByText("ok")).toBeTruthy()
  expect(within(rows[1]!).getByText("policy:read")).toBeTruthy()
  expect(within(rows[1]!).getByText("missing")).toBeTruthy()
  expect(within(rows[1]!).getByText("cloud.permission_missing")).toBeTruthy()
  expect(within(rows[2]!).getByText("auth_keys:write")).toBeTruthy()
  expect(within(rows[2]!).getByText("unverified")).toBeTruthy()
})

test("a capability missing because the provider could not be reached is shown as could not check", () => {
  render(
    <CapabilityList
      capabilities={[
        { name: "devices:read", state: "missing", detail: "cloud.unavailable" },
        { name: "policy:read", state: "missing", detail: "cloud.rate_limited" },
      ]}
    />,
  )
  for (const row of clRows()) {
    expect(within(row).getByText("could not check")).toBeTruthy()
    expect(within(row).queryByText("missing")).toBeNull()
  }
})

test("a missing capability without a detail shows no detail text", () => {
  render(<CapabilityList capabilities={[{ name: "devices:read", state: "missing" }]} />)
  const row = clRows()[0]!
  expect(within(row).getByText("missing")).toBeTruthy()
  expect(row.textContent).toBe("devices:readmissing")
})

test("no capabilities say so", () => {
  render(<CapabilityList capabilities={[]} />)
  expect(screen.getByText("No capabilities reported.")).toBeTruthy()
  expect(screen.queryByRole("list")).toBeNull()
})

test("the list has no accessibility violations", async () => {
  const { container } = render(<CapabilityList capabilities={clCaps} />)
  expect(await axe(container)).toHaveNoViolations()
})
