import { useState } from "react"
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { renderWithProviders } from "@/test/utils"
import { Field, MarkBadges } from "./list-parts"

function LpField() {
  const [value, setValue] = useState("")
  return <Field id="lp-name" label="Name" value={value} onChange={setValue} help="Use letters only." invalid={value === "x"} />
}

test("a reserved object is marked with its owner", () => {
  const { container } = renderWithProviders(<MarkBadges reserved="npm.admin.expose" unclaimed={false} />)
  expect(container.textContent).toContain("Reserved: npm.admin.expose")
  expect(container.textContent).not.toContain("Unclaimed")
})

test("an unclaimed object is marked as an unclaimed Darkpanel object", () => {
  const { container } = renderWithProviders(<MarkBadges reserved="" unclaimed />)
  expect(container.textContent).toContain("Unclaimed Darkpanel object")
  expect(container.textContent).not.toContain("Reserved")
})

test("an ordinary object has no mark", () => {
  const { container } = renderWithProviders(<MarkBadges reserved="" unclaimed={false} />)
  expect(container.textContent).not.toContain("Reserved")
  expect(container.textContent).not.toContain("Unclaimed")
})

test("a field has a label, a help line that describes it and an invalid state", async () => {
  renderWithProviders(<LpField />)
  const input = screen.getByLabelText("Name")
  expect(input.getAttribute("aria-describedby")).toBe("lp-name-help")
  expect(screen.getByText("Use letters only.").getAttribute("id")).toBe("lp-name-help")
  expect(input.getAttribute("aria-invalid")).not.toBe("true")
  await userEvent.setup().type(input, "x")
  expect(input.getAttribute("aria-invalid")).toBe("true")
})
