import { getRoles, queryAllByRole } from "@testing-library/dom"
import type { UserEvent } from "@testing-library/user-event"
import { axe } from "vitest-axe"

// Runs axe on el and fails with the rule ids and targets of every violation.
export async function expectNoAxeViolations(el: Element): Promise<void> {
  const { violations } = await axe(el)
  if (violations.length === 0) return
  const lines = violations.map(
    (v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`,
  )
  throw new Error(`axe found ${violations.length} violation(s):\n${lines.join("\n")}`)
}

// The accessible name of document.activeElement, "" when it is body.
export function focusedName(): string {
  const el = document.activeElement
  if (!(el instanceof HTMLElement) || el === document.body) return ""
  // An element under aria-hidden still has focus to name, and one can be
  // listed under several implicit roles, so every role it has is tried.
  const roles = Object.entries(getRoles(document.body, { hidden: true }))
    .filter(([, elements]) => elements.includes(el))
    .map(([role]) => role)
  // Role queries skip elements that aria-query gives no role, such as password
  // inputs, so those are named from their labels directly.
  if (roles.length === 0) return labelName(el)
  for (const role of roles) {
    let name = ""
    queryAllByRole(document.body, role, {
      hidden: true,
      name: (accessibleName, node) => {
        if (node === el) name = accessibleName
        return false
      },
    })
    if (name !== "") return name
  }
  return ""
}

function labelName(el: HTMLElement): string {
  const ids = (el.getAttribute("aria-labelledby") ?? "").split(/\s+/).filter((id) => id !== "")
  const labelledBy = ids
    .map((id) => document.getElementById(id))
    .filter((label) => label !== null)
    .map((label) => label.textContent.trim())
    .join(" ")
    .trim()
  if (labelledBy !== "") return labelledBy
  const ariaLabel = (el.getAttribute("aria-label") ?? "").trim()
  if (ariaLabel !== "") return ariaLabel
  if ("labels" in el && el.labels !== null) {
    return Array.from(el.labels as NodeListOf<HTMLLabelElement>, (label) => label.textContent.trim())
      .join(" ")
      .trim()
  }
  return ""
}

// Presses Tab (or Shift+Tab when backwards) count times and returns the
// accessible name of document.activeElement after each press ("" when none).
export async function tabSequence(
  user: UserEvent,
  count: number,
  opts: { backwards?: boolean } = {},
): Promise<string[]> {
  const names: string[] = []
  for (let i = 0; i < count; i++) {
    await user.tab({ shift: opts.backwards === true })
    names.push(focusedName())
  }
  return names
}
