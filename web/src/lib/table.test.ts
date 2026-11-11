import { expect, test } from "vitest"
import { focusableScroller } from "./table"

test("UX-12: a table's scrolling wrapper becomes a named, focusable region", () => {
  const wrapper = document.createElement("div")
  const table = document.createElement("table")
  wrapper.append(table)
  focusableScroller("Update history")(table)
  expect(wrapper.getAttribute("tabindex")).toBe("0")
  expect(wrapper.getAttribute("role")).toBe("region")
  expect(wrapper.getAttribute("aria-label")).toBe("Update history")
})
