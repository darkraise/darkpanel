import { expect, test } from "vitest"
import { stateVariant } from "./badges"

test("UI-4: each host and rollout state maps to its badge variant", () => {
  const cases: [string, string][] = [
    ["committed", "default"], ["succeeded", "default"],
    ["updating", "secondary"], ["running", "secondary"], ["draining", "secondary"], ["waiting", "secondary"],
    ["failed", "destructive"], ["quarantined", "destructive"],
    ["skipped", "outline"], ["cancelled", "outline"], ["anything", "outline"],
  ]
  for (const [state, variant] of cases) expect(stateVariant(state)).toBe(variant)
})
