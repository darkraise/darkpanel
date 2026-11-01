import { useState } from "react"
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders } from "@/test/utils"
import TailnetHostCreate from "./host-create"

type HcValue = Record<string, unknown>

// hcRender keeps the value the way the Add host dialog does: whatever the
// component reports becomes the value it is given next.
function hcRender(initial: HcValue = {}) {
  const onChange = vi.fn()
  function Harness() {
    const [value, setValue] = useState<HcValue>(initial)
    return (
      <TailnetHostCreate
        value={value}
        onChange={(v) => {
          onChange(v)
          setValue(v)
        }}
      />
    )
  }
  const result = renderWithProviders(<Harness />)
  return { onChange, ...result }
}

const HC_TAGS = "Tags (comma-separated)"

test("offers to join the tailnet, unchecked, with no tags input", () => {
  hcRender()
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: false })).toBeTruthy()
  expect(screen.queryByLabelText(HC_TAGS)).toBeNull()
})

test("checking the box asks for tags and reports a join with none yet", async () => {
  const { onChange } = hcRender()
  await userEvent.setup().click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  expect(onChange).toHaveBeenLastCalledWith({ tailnetJoin: true, tags: [] })
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: true })).toBeTruthy()
  expect(screen.getByLabelText(HC_TAGS)).toBeTruthy()
})

test("typed tags are trimmed, de-duplicated and reported as a list", async () => {
  const { onChange } = hcRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(screen.getByLabelText(HC_TAGS), "tag:server, tag:ci ,, tag:server")
  expect(onChange).toHaveBeenLastCalledWith({ tailnetJoin: true, tags: ["tag:server", "tag:ci"] })
})

test("unchecking the box reports no input and hides the tags", async () => {
  const { onChange } = hcRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(screen.getByLabelText(HC_TAGS), "tag:server")
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  expect(onChange).toHaveBeenLastCalledWith({})
  expect(screen.queryByLabelText(HC_TAGS)).toBeNull()
})

test("shows the value it is given", () => {
  hcRender({ tailnetJoin: true, tags: ["tag:server", "tag:ci"] })
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: true })).toBeTruthy()
  expect((screen.getByLabelText(HC_TAGS) as HTMLInputElement).value).toBe("tag:server, tag:ci")
})

test("a tag that is not tag:<name> is flagged", async () => {
  hcRender({ tailnetJoin: true, tags: [] })
  const user = userEvent.setup()
  const input = screen.getByLabelText(HC_TAGS)
  expect(screen.queryByText("Each tag looks like tag:server.")).toBeNull()
  await user.type(input, "server")
  expect(screen.getByText("Each tag looks like tag:server.")).toBeTruthy()
  expect(input.getAttribute("aria-invalid")).toBe("true")
  await user.clear(input)
  await user.type(input, "tag:server")
  expect(screen.queryByText("Each tag looks like tag:server.")).toBeNull()
})

test("the join input has no accessibility violations", async () => {
  const { container } = hcRender({ tailnetJoin: true, tags: ["tag:server"] })
  expect(await axe(container)).toHaveNoViolations()
})
