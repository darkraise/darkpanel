/// <reference types="node" />
import { readdirSync, readFileSync, existsSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..")

const FLOWS = [
  "Login with TOTP",
  "First-admin and lockout recovery pages",
  "Host list",
  "Firewall change with confirm and revert",
  "Container actions",
  "Expose a container",
]

const CRITERIA: Record<string, string> = {
  "1.1.1": "A",
  "1.3.1": "A",
  "1.4.3": "AA",
  "1.4.4": "AA",
  "1.4.10": "AA",
  "1.4.11": "AA",
  "2.1.1": "A",
  "2.1.2": "A",
  "2.4.2": "A",
  "2.4.3": "A",
  "2.4.7": "AA",
  "3.3.1": "A",
  "3.3.2": "A",
  "4.1.2": "A",
  "4.1.3": "AA",
}

const COLUMNS = ["Criterion", "Level", "Method", "Evidence"]

interface Row {
  flow: string
  criterion: string
  level: string
  method: string
  evidence: string[]
}

function record(): string {
  return readFileSync(path.join(root, "docs", "accessibility.md"), "utf8")
}

// section is the text under the "## <title>" heading, up to the next "## " heading.
function section(doc: string, title: string): string {
  const lines = doc.split("\n")
  const start = lines.findIndex((line) => line === `## ${title}`)
  if (start < 0) return ""
  const end = lines.findIndex((line, i) => i > start && line.startsWith("## "))
  return lines.slice(start + 1, end < 0 ? undefined : end).join("\n")
}

function cells(line: string): string[] {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cell.trim())
}

// tables returns the header and rows of the table under each "### <flow>"
// heading of the Criteria section.
function tables(doc: string): Map<string, { header: string[]; rows: Row[] }> {
  const found = new Map<string, { header: string[]; rows: Row[] }>()
  let flow = ""
  for (const line of section(doc, "Criteria").split("\n")) {
    if (line.startsWith("### ")) {
      flow = line.slice(4).trim()
      found.set(flow, { header: [], rows: [] })
      continue
    }
    const table = found.get(flow)
    if (table === undefined || !line.trim().startsWith("|")) continue
    const row = cells(line)
    if (table.header.length === 0) {
      table.header = row
    } else if (!row.every((cell) => /^:?-+:?$/.test(cell))) {
      const [criterion = "", level = "", method = "", evidence = ""] = row
      table.rows.push({ flow, criterion, level, method, evidence: [...evidence.matchAll(/`([^`]+)`/g)].map((m) => m[1] ?? "") })
    }
  }
  return found
}

function allRows(): Row[] {
  return [...tables(record()).values()].flatMap((t) => t.rows)
}

describe("docs/accessibility.md", () => {
  it("names the six core flows in Scope", () => {
    const scope = section(record(), "Scope")
    for (const flow of FLOWS) expect(scope, `Scope does not name ${flow}`).toContain(flow)
  })

  it("has one table of criteria for each flow, with the four columns", () => {
    const found = tables(record())
    expect([...found.keys()]).toEqual(FLOWS)
    for (const [flow, table] of found) {
      expect(table.header, `table of ${flow}`).toEqual(COLUMNS)
      expect(table.rows.length, `rows of ${flow}`).toBeGreaterThan(0)
    }
  })

  it("accounts for each criterion, at its level, in a table or in Known issues", () => {
    const rows = allRows()
    const seen = new Set<string>()
    for (const row of rows) {
      const number = /^(\d\.\d\.\d+)\b/.exec(row.criterion)?.[1] ?? ""
      expect(Object.keys(CRITERIA), `${row.flow}: unknown criterion "${row.criterion}"`).toContain(number)
      expect(row.level, `${row.flow}: level of ${number}`).toBe(CRITERIA[number])
      seen.add(number)
    }
    for (const found of section(record(), "Known issues").matchAll(/\b(\d\.\d\.\d+)\b/g)) {
      if (found[1] !== undefined && found[1] in CRITERIA) seen.add(found[1])
    }
    expect([...seen].sort(), "criteria that no table and no known issue names").toEqual(Object.keys(CRITERIA).sort())
  })

  it("marks every row automated or manual", () => {
    for (const row of allRows()) {
      expect(["automated", "manual"], `${row.flow}: ${row.criterion}`).toContain(row.method)
    }
  })

  it("cites existing files as evidence on every row", () => {
    for (const row of allRows()) {
      expect(row.evidence.length, `${row.flow}: ${row.criterion} has no evidence`).toBeGreaterThan(0)
      for (const file of row.evidence) {
        expect(existsSync(path.join(root, file)), `${row.flow}: ${row.criterion}: ${file} does not exist`).toBe(true)
      }
    }
  })

  it("cites every accessibility test file under web/src", () => {
    const cited = new Set(allRows().flatMap((row) => row.evidence))
    const tests = readdirSync(path.join(root, "web", "src"), { recursive: true, encoding: "utf8" })
      .filter((file) => file.endsWith(".a11y.test.tsx"))
      .map((file) => `web/src/${file.split(path.sep).join("/")}`)
    expect(tests.length, "no *.a11y.test.tsx file found").toBeGreaterThan(0)
    for (const file of tests) expect(cited, `${file} is not cited as evidence`).toContain(file)
  })

  it("has a Known issues section", () => {
    expect(section(record(), "Known issues").trim()).not.toBe("")
  })
})
