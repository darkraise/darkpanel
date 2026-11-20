import { execFileSync } from "node:child_process"
import { existsSync, globSync, readFileSync } from "node:fs"
import { render } from "@testing-library/react"
import { Server } from "lucide-react"
import { describe, expect, test } from "vitest"
import { InfoTip } from "@/components/visual/info-tip"
import { Gauge } from "@/components/visual/gauge"
import { ModuleIcon } from "@/components/visual/module-icon"
import { ResourceCard } from "@/components/visual/resource-card"
import { SectionHeader } from "@/components/visual/section-header"
import { StatTile } from "@/components/visual/stat-tile"
import { StatusDonut } from "@/components/visual/status-donut"
import { StatusDot } from "@/components/visual/status-dot"
import { StatusPill } from "@/components/visual/status-pill"
import { Stepper } from "@/components/visual/stepper"
import { UsageBar } from "@/components/visual/usage-bar"
import { allowlist } from "@/test/copy-allowlist"
import { colourHits, longTexts } from "@/test/design-guards"
import { expectNoAxeViolations } from "@/test/a11y"

// Acceptance tests of the ui-revamp-server-management spec, web unit layer.

// A1 ---------------------------------------------------------------------

describe("A1: darkraise-ui 7.2.2", () => {
  test("A1: the lockfile resolves darkraise-ui to 7.2.2 and nothing else", () => {
    const lock = readFileSync("pnpm-lock.yaml", "utf8")
    const versions = [...lock.matchAll(/^\s+darkraise-ui@(\d+\.\d+\.\d+)[:(]/gm)].map((m) => m[1])
    expect(versions.length).toBeGreaterThan(0)
    expect(new Set(versions)).toEqual(new Set(["7.2.2"]))
    expect(lock).toMatch(/darkraise-ui:\s*\n\s+specifier: 7\.2\.2\s*\n\s+version: 7\.2\.2/)
    expect(JSON.parse(readFileSync("node_modules/darkraise-ui/package.json", "utf8")).version).toBe("7.2.2")
  })

  test("A1: no 7.0 removed key or token remains in web/src or web/public", () => {
    const files = [...globSync("src/**/*.{ts,tsx,css}"), ...globSync("public/**/*")].filter((f) => !f.includes(".test.") && !f.includes(".acceptance."))
    const removed: [string, RegExp][] = [
      ["buttonElevation config key", /\bbuttonElevation\b/],
      ["elevation: \"medium\" config value", /\belevation\s*:\s*["']medium["']/],
      ["unsuffixed status token var(--destructive)", /var\(--(?:color-)?(?:destructive|success|warning|info)(?:-foreground)?\)/],
      ["Badge size=\"md\"", /<Badge\b[^>]*\bsize=["']md["']/],
      ["TableCell align=\"right\"", /<TableCell\b[^>]*\balign=["']right["']/],
      ["@fontsource-variable import", /@fontsource-variable/],
    ]
    const hits: string[] = []
    for (const f of files) {
      if (!existsSync(f)) continue
      let text = ""
      try {
        text = readFileSync(f, "utf8")
      } catch {
        continue
      }
      for (const [what, re] of removed) if (re.test(text)) hits.push(`${f}: ${what}`)
    }
    expect(hits).toEqual([])
  })

  test("A1: pnpm -C web build succeeds", { timeout: 600_000 }, () => {
    execFileSync("pnpm", ["run", "build"], { cwd: process.cwd(), stdio: "pipe", timeout: 580_000, env: process.env })
  })
})

// A6 ---------------------------------------------------------------------

// accessibleText is the text a screen reader can reach in el: its text, the
// names and titles of its parts.
function accessibleText(el: Element): string {
  const parts = [el.textContent ?? ""]
  for (const n of el.querySelectorAll("[aria-label],[title],title")) parts.push(n.getAttribute("aria-label") ?? "", n.getAttribute("title") ?? "", n.textContent ?? "")
  return parts.join(" ")
}

const visuals: { name: string; element: () => React.ReactElement; wants: string[] }[] = [
  { name: "StatusDot", element: () => <StatusDot status="warn" label="Pending reboot" />, wants: ["Pending reboot"] },
  { name: "StatusPill", element: () => <StatusPill status="ok" label="Online" />, wants: ["Online"] },
  { name: "Gauge", element: () => <Gauge value={6.2} max={8} label="Memory" unit="GiB" />, wants: ["Memory", "6.2", "8", "GiB"] },
  { name: "UsageBar", element: () => <UsageBar used={41} total={100} label="Disk" unit="GiB" />, wants: ["Disk", "41", "100", "GiB"] },
  { name: "StatTile", element: () => <StatTile icon={Server} figure={42} caption="Updates" />, wants: ["42", "Updates"] },
  {
    name: "StatusDonut",
    element: () => <StatusDonut label="Containers" segments={[{ status: "ok", label: "Running", value: 5 }, { status: "off", label: "Stopped", value: 2 }]} />,
    wants: ["Containers", "Running", "5", "Stopped", "2"],
  },
  { name: "ResourceCard", element: () => <ResourceCard icon={Server} title="web-1" status={{ status: "ok", label: "Online" }} figures={<span>3 GiB</span>} />, wants: ["web-1", "Online", "3 GiB"] },
  {
    name: "Stepper",
    element: () => <Stepper steps={[{ id: "plan", label: "Plan" }, { id: "confirm", label: "Confirm" }, { id: "apply", label: "Apply" }]} current="confirm" />,
    wants: ["Plan", "Confirm", "Apply"],
  },
  { name: "InfoTip", element: () => <InfoTip label="About updates">Held packages are skipped.</InfoTip>, wants: ["About updates"] },
  { name: "SectionHeader", element: () => <SectionHeader icon={Server} title="Updates" status={{ status: "warn", label: "3 pending" }} />, wants: ["Updates", "3 pending"] },
  { name: "ModuleIcon", element: () => <ModuleIcon module="docker" />, wants: ["Docker"] },
]

describe("A6: visual components", () => {
  for (const mode of ["light", "dark"]) {
    for (const v of visuals) {
      test(`A6: ${v.name} renders its value as accessible text and passes axe in ${mode} mode`, async () => {
        document.documentElement.className = mode
        document.documentElement.dataset.mode = mode
        const { container } = render(<main>{v.element()}</main>)
        const text = accessibleText(container)
        for (const want of v.wants) expect(text, `${v.name} lacks ${want}`).toContain(want)
        await expectNoAxeViolations(container)
      })
    }
  }
})

// A7, A8 -----------------------------------------------------------------

const sourceFiles = globSync("src/**/*.{ts,tsx,css}").filter((f) => !f.endsWith("theme.config.ts") && !f.includes(".test.") && !f.includes(".acceptance."))

describe("A8: the colour guard", () => {
  test("A8: no colour literal in web/src outside theme.config.ts", () => {
    expect(sourceFiles.flatMap((f) => colourHits(f, readFileSync(f, "utf8")))).toEqual([])
  })
  test("A8: the guard fails on each kind of colour literal", () => {
    for (const lit of ['"#fff"', '"#1e90ffcc"', '"rgb(1, 2, 3)"', '"rgba(1,2,3,.5)"', '"hsl(10 20% 30%)"', '"oklch(0.7 0.1 200)"']) {
      expect(colourHits("x.tsx", `const c = ${lit}`), lit).toHaveLength(1)
    }
    expect(colourHits("x.css", "a { color: #abc; }")).toHaveLength(1)
    expect(colourHits("x.tsx", 'const c = "var(--color-primary)"')).toHaveLength(0)
  })
})

describe("A7: the copy guard", () => {
  const long = "w".repeat(91)
  const ok = "w".repeat(90)
  test("A7: the repository has no visible text over 90 characters outside the allowlist", () => {
    const hits = sourceFiles
      .filter((f) => f.endsWith(".tsx"))
      .flatMap((f) => longTexts(f, readFileSync(f, "utf8"), 90))
      .filter((h) => !allowlist.some((a) => a.file === h.file && h.text.startsWith(a.startsWith)))
    expect(hits).toEqual([])
  })
  test("A7: the guard fails on a visible text over 90 characters and not on 90", () => {
    expect(longTexts("x.tsx", `<p>${long}</p>`, 90)).toHaveLength(1)
    expect(longTexts("x.tsx", `<p>${ok}</p>`, 90)).toHaveLength(0)
    expect(longTexts("x.tsx", `<Badge>${long}</Badge>`, 90)).toHaveLength(1)
    expect(longTexts("x.tsx", `<EmptyState title="${long}" />`, 90)).toHaveLength(1)
  })
  test("A7: text in an InfoTip or a dialog body is outside the budget", () => {
    expect(longTexts("x.tsx", `<InfoTip label="i">${long}</InfoTip>`, 90)).toHaveLength(0)
    expect(longTexts("x.tsx", `<DialogContent><DialogDescription>${long}</DialogDescription></DialogContent>`, 90)).toHaveLength(0)
  })
  test("A7: a template literal is measured by its static parts only", () => {
    const fifty = "y".repeat(50)
    expect(longTexts("x.tsx", "<p>{`" + fifty + "${n}" + fifty + "`}</p>", 90)).toHaveLength(1)
    expect(longTexts("x.tsx", "<p>{`" + "y".repeat(40) + "${someVeryLongRuntimeValueThatWouldMakeItLong}" + "y".repeat(40) + "`}</p>", 90)).toHaveLength(0)
  })
})
