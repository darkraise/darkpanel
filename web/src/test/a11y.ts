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
  const role = Object.entries(getRoles(document.body)).find(([, elements]) =>
    elements.includes(el),
  )?.[0]
  if (role === undefined) return ""
  let name = ""
  queryAllByRole(document.body, role, {
    hidden: true,
    name: (accessibleName, node) => {
      if (node === el) name = accessibleName
      return false
    },
  })
  return name
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
