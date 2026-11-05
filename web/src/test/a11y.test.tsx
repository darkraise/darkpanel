import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "./a11y"

function Form() {
  return (
    <form>
      <label htmlFor="a11y-name">Name</label>
      <input id="a11y-name" />
      <button type="button" aria-label="Close">
        x
      </button>
      <a href="/docs">Docs</a>
    </form>
  )
}

async function failure(promise: Promise<void>): Promise<string> {
  try {
    await promise
  } catch (err) {
    return err instanceof Error ? err.message : String(err)
  }
  return ""
}

test("expectNoAxeViolations resolves for a labelled form", async () => {
  const { container } = render(<Form />)
  await expectNoAxeViolations(container)
})

test("expectNoAxeViolations names the rule and the target of every violation", async () => {
  const { container } = render(
    <div>
      <input id="a11y-bare" />
      <img id="a11y-img" src="x.png" />
    </div>,
  )
  const message = await failure(expectNoAxeViolations(container))
  expect(message).toMatch(/^axe found 2 violation\(s\):/)
  expect(message).toMatch(/label: .*#a11y-bare/)
  expect(message).toMatch(/image-alt: .*#a11y-img/)
})

test("tabSequence returns the accessible name after each Tab press", async () => {
  render(<Form />)
  expect(await tabSequence(userEvent.setup(), 3)).toEqual(["Name", "Close", "Docs"])
})

test("tabSequence goes backwards from the end with Shift+Tab", async () => {
  render(<Form />)
  expect(await tabSequence(userEvent.setup(), 2, { backwards: true })).toEqual(["Docs", "Close"])
})

test("focusedName is empty while the body has focus and follows focus", () => {
  render(<Form />)
  expect(focusedName()).toBe("")
  screen.getByRole("link", { name: "Docs" }).focus()
  expect(focusedName()).toBe("Docs")
})

test("focusedName prefers aria-labelledby over the content", () => {
  render(
    <div>
      <span id="a11y-label">Delete host web-1</span>
      <button aria-labelledby="a11y-label">Delete</button>
    </div>,
  )
  screen.getByRole("button").focus()
  expect(focusedName()).toBe("Delete host web-1")
})

test("focusedName names a labelled field that has no role", () => {
  render(
    <form>
      <label htmlFor="a11y-secret">Password</label>
      <input id="a11y-secret" type="password" />
    </form>,
  )
  screen.getByLabelText("Password").focus()
  expect(focusedName()).toBe("Password")
})
