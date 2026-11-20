import { globSync, readFileSync } from "node:fs"
import { expect, test } from "vitest"
import { allowlist } from "./copy-allowlist"
import { colourHits, longTexts } from "./design-guards"

const files = globSync("src/**/*.{ts,tsx,css}").filter((f) => !f.endsWith("theme.config.ts") && !f.includes(".test."))

test("A8: no colour literal outside theme.config.ts", () => {
  expect(files.flatMap((f) => colourHits(f, readFileSync(f, "utf8")))).toEqual([])
  expect(colourHits("x.tsx", 'const c = "#1e90ff"')).toHaveLength(1)
  expect(colourHits("x.css", "color: oklch(0.7 0.1 200);")).toHaveLength(1)
})

test("A7: no visible text over 90 characters outside InfoTip, dialog bodies and the allowlist", () => {
  const hits = files.filter((f) => f.endsWith(".tsx")).flatMap((f) => longTexts(f, readFileSync(f, "utf8"), 90)).filter((h) => !allowlist.some((a) => a.file === h.file && h.text.startsWith(a.startsWith)))
  expect(hits).toEqual([])
  const long = "x".repeat(91)
  expect(longTexts("x.tsx", `<p>${long}</p>`, 90)).toHaveLength(1)
  expect(longTexts("x.tsx", `<StatTile caption="${long}" />`, 90)).toHaveLength(1)
  expect(longTexts("x.tsx", `<InfoTip label="i">${long}</InfoTip>`, 90)).toHaveLength(0)
  expect(longTexts("x.tsx", "<p>{`" + "y".repeat(50) + "${n}" + "y".repeat(50) + "`}</p>", 90)).toHaveLength(1)
})
