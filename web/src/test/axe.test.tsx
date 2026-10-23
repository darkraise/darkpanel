import { render } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"

test("axe passes a labelled control", async () => {
  const { container } = render(
    <form>
      <label htmlFor="axe-name">Name</label>
      <input id="axe-name" />
      <button type="submit">Save</button>
    </form>,
  )
  expect(await axe(container)).toHaveNoViolations()
})

test("axe reports an unlabelled control", async () => {
  const { container } = render(
    <form>
      <input />
    </form>,
  )
  const results = await axe(container)
  expect(() => expect(results).toHaveNoViolations()).toThrow(/label/)
})
