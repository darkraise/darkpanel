import type { AccessClientInput } from "./types"

// parseDomains splits text on commas and whitespace, lowercases it and keeps
// the first of any repeated name, in order.
export function parseDomains(text: string): string[] {
  const names: string[] = []
  for (const part of text.split(/[\s,]+/)) {
    const name = part.toLowerCase()
    if (name !== "" && !names.includes(name)) names.push(name)
  }
  return names
}

// parsePort returns the whole number 1 to 65535 the text spells in digits,
// or null.
export function parsePort(text: string): number | null {
  const t = text.trim()
  if (!/^\d+$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 65535 ? n : null
}

// parseOptionalId returns 0 for blank text, the positive whole number the
// text spells in digits, or null.
export function parseOptionalId(text: string): number | null {
  const t = text.trim()
  if (t === "") return 0
  if (!/^\d+$/.test(t)) return null
  const n = Number(t)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}

export type RulesResult = { ok: true; clients: AccessClientInput[] } | { ok: false; error: string }

// parseRules reads one "allow|deny <address>" rule per line. Whether the
// address is an IP or a CIDR is left to the action.
export function parseRules(text: string): RulesResult {
  const clients: AccessClientInput[] = []
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const line = (lines[i] ?? "").trim()
    if (line === "") continue
    const tokens = line.split(/\s+/)
    const directive = (tokens[0] ?? "").toLowerCase()
    const address = tokens[1]
    if (tokens.length !== 2 || address === undefined || (directive !== "allow" && directive !== "deny")) {
      return { ok: false, error: `Line ${i + 1}: start with allow or deny, then an address.` }
    }
    clients.push({ address, directive })
  }
  if (clients.length === 0) return { ok: false, error: "Add at least one rule." }
  return { ok: true, clients }
}
