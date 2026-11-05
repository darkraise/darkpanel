import { render } from "@testing-library/react"
import { beforeEach, expect, test } from "vitest"
import { usePageTitle } from "./page-title"

function Page({ title }: { title: string }) {
  usePageTitle(title)
  return null
}

beforeEach(() => {
  document.title = "Before"
})

test.each([
  ["Hosts", "Hosts · Darkpanel"],
  ["", "Darkpanel"],
])("usePageTitle(%j) sets the title to %j", (title, want) => {
  render(<Page title={title} />)
  expect(document.title).toBe(want)
})

test("usePageTitle follows a changed title", () => {
  const { rerender } = render(<Page title="Hosts" />)
  rerender(<Page title="Audit log" />)
  expect(document.title).toBe("Audit log · Darkpanel")
})

test("usePageTitle restores the previous title on unmount", () => {
  const { unmount } = render(<Page title="Hosts" />)
  unmount()
  expect(document.title).toBe("Before")
})

test("a page that replaces another in one commit keeps its own title", () => {
  const { rerender, unmount } = render(<Page key="a" title="Hosts" />)
  rerender(<Page key="b" title="Audit log" />)
  expect(document.title).toBe("Audit log · Darkpanel")
  unmount()
  expect(document.title).toBe("Before")
})
