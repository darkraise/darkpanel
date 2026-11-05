import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"
import { expect, test, type Page } from "@playwright/test"
import { CONTAINER_ID, HOST_ID, mockApi } from "./fixtures"

// The port reaches the tests through the environment: Vite picks a free one
// and playwright.config.ts captures it from the server's output.
test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const shots = fileURLToPath(new URL("../../docs/accessibility/", import.meta.url))

type Theme = "light" | "dark"
const THEMES: Theme[] = ["light", "dark"]

interface View {
  // The <flow> of the screenshot names; the flows of spec section 9 are
  // login, hosts, firewall-change-*, container-* and expose-plan.
  slug: string
  signedIn: boolean
  open: (page: Page) => Promise<void>
  // The title of the change dialog this view ends in, and the controls whose
  // accessible names it must offer.
  dialog?: { title: string; controls: [role: "button" | "checkbox" | "textbox" | "spinbutton", name: string][] }
}

async function openFirewallPlan(page: Page): Promise<void> {
  await page.goto(`/hosts/${HOST_ID}/firewall`)
  await page.getByRole("button", { name: "Add rule" }).click()
  await page.getByLabel("To port").fill("443")
  await page.getByRole("button", { name: "Plan", exact: true }).click()
  await expect(page.getByRole("checkbox", { name: "Override firewall.lockout" })).toBeVisible()
}

async function openContainerPage(page: Page): Promise<void> {
  await page.goto(`/hosts/${HOST_ID}/docker/containers/${CONTAINER_ID}`)
  await expect(page.getByRole("form", { name: "Expose container" })).toBeVisible()
  await expect(page.getByText("[warn] upstream timed out")).toBeVisible()
  await expect(page.getByLabel("Container usage")).toBeVisible()
}

const VIEWS: View[] = [
  {
    slug: "login",
    signedIn: false,
    open: async (page) => {
      await page.goto("/login")
      await page.getByLabel("Username").fill("admin")
      await page.getByLabel("Password", { exact: true }).fill("placeholder")
      await page.getByLabel("Authentication code").fill("000000")
      await page.getByRole("button", { name: "Log in" }).click()
      await expect(page.getByRole("alert")).toContainText("Login failed")
    },
  },
  {
    slug: "hosts",
    signedIn: true,
    open: async (page) => {
      await page.goto("/")
      await expect(page.getByRole("table")).toContainText("This controller")
      await expect(page.getByRole("table")).toContainText("Journal ingestion failing")
    },
  },
  {
    slug: "firewall-change-plan",
    signedIn: true,
    open: openFirewallPlan,
    dialog: {
      title: "Add a rule",
      controls: [
        ["checkbox", "Override firewall.lockout"],
        ["spinbutton", "Revert after (seconds)"],
        ["button", "Apply"],
      ],
    },
  },
  {
    slug: "firewall-change-applied",
    signedIn: true,
    open: async (page) => {
      await openFirewallPlan(page)
      await page.getByRole("checkbox", { name: "Override firewall.lockout" }).click()
      await page.getByRole("button", { name: "Apply" }).click()
      await expect(page.getByRole("timer")).toContainText("Reverts automatically in")
    },
    dialog: {
      title: "Add a rule",
      controls: [
        ["button", "Confirm"],
        ["button", "Revert"],
      ],
    },
  },
  {
    slug: "container-actions-list",
    signedIn: true,
    open: async (page) => {
      await page.goto(`/hosts/${HOST_ID}/docker`)
      await expect(page.getByRole("table", { name: "Docker containers" })).toContainText("worker")
    },
  },
  {
    slug: "container-actions-dialog",
    signedIn: true,
    open: async (page) => {
      await page.goto(`/hosts/${HOST_ID}/docker`)
      await page.getByRole("button", { name: "Stop web" }).click()
      await expect(page.getByRole("textbox", { name: "Type web to confirm" })).toBeVisible()
    },
    dialog: {
      title: "Stop web",
      controls: [
        ["textbox", "Type web to confirm"],
        ["button", "Apply"],
      ],
    },
  },
  {
    slug: "container-page",
    signedIn: true,
    open: openContainerPage,
  },
  {
    slug: "expose-plan",
    signedIn: true,
    open: async (page) => {
      await openContainerPage(page)
      await page.getByLabel("Domains").fill("app.example.com")
      await page.getByRole("form", { name: "Expose container" }).getByRole("button", { name: "Plan" }).click()
      await expect(page.getByRole("dialog", { name: "Expose web at app.example.com" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Apply" })).toBeEnabled()
    },
    dialog: {
      title: "Expose web at app.example.com",
      controls: [["button", "Apply"]],
    },
  },
]

// A view is ready for measuring once its dialog animation has finished, so
// axe never samples a half-transparent element.
async function settle(page: Page, theme: Theme): Promise<void> {
  await expect(page.locator("html")).toHaveAttribute("data-mode", theme)
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => undefined)),
    ),
  )
}

async function visit(page: Page, view: View, theme: Theme): Promise<string[]> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: view.signedIn })
  await view.open(page)
  await settle(page, theme)
  return fixtures.unhandled
}

interface AxeNode {
  target: unknown[]
  any: { message: string }[]
}

async function contrastViolations(page: Page): Promise<string[]> {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: { id: string; nodes: AxeNode[] }[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "rule", values: ["color-contrast"] }, resultTypes: ["violations"] })
    return result.violations
  })
  return violations.flatMap((v) => v.nodes.map((n) => `${v.id}: ${n.target.join(" ")}: ${n.any[0]?.message ?? ""}`))
}

async function scrollExtent(page: Page): Promise<{ scrollWidth: number; clientWidth: number }> {
  return page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }))
}

// The page may not scroll sideways while an open dialog still clips its own
// content, because a dialog is fixed; this lists what lies outside the
// viewport, leaving out what a scrolling ancestor inside the dialog holds.
async function clippedInDialog(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const dialog = document.querySelector('[role="dialog"]')
    if (dialog === null) return []
    const width = document.documentElement.clientWidth
    const clipped: string[] = []
    for (const el of Array.from(dialog.querySelectorAll("*"))) {
      const box = el.getBoundingClientRect()
      if (box.width === 0 || (box.left >= -0.5 && box.right <= width + 0.5)) continue
      let scrolls = false
      for (let parent = el.parentElement; parent !== null && parent !== dialog; parent = parent.parentElement) {
        const overflow = getComputedStyle(parent).overflowX
        if (overflow === "auto" || overflow === "scroll") scrolls = true
      }
      if (!scrolls) clipped.push(`${el.tagName.toLowerCase()}.${el.getAttribute("class") ?? ""} spans ${Math.round(box.left)} to ${Math.round(box.right)} of ${width}`)
    }
    return clipped
  })
}

const CHECKS = [
  // Axe's contrast rules at the size of a desktop window.
  { check: "contrast", viewport: { width: 1280, height: 800 }, scale: 1 },
  // WCAG 1.4.10 sets the reflow width at 320 CSS px.
  { check: "reflow-320", viewport: { width: 320, height: 640 }, scale: 1 },
  // 200 % browser zoom on a 1280x800 window is a 640x400 CSS px viewport with
  // twice the device pixels; emulating it that way, rather than with CSS
  // zoom, keeps media queries and the layout viewport what a zoomed browser
  // reports.
  { check: "zoom-200", viewport: { width: 640, height: 400 }, scale: 2 },
] as const

for (const { check, viewport, scale } of CHECKS) {
  test.describe(check, () => {
    test.use({ viewport, deviceScaleFactor: scale })
    for (const theme of THEMES) {
      for (const view of VIEWS) {
        test(`${view.slug} ${theme}`, async ({ page }) => {
          const unhandled = await visit(page, view, theme)
          await page.screenshot({ path: `${shots}${view.slug}-${theme}-${check}.png` })
          expect(unhandled, "API requests without a fixture").toEqual([])
          if (check === "contrast") {
            expect(await contrastViolations(page)).toEqual([])
          } else {
            const { scrollWidth, clientWidth } = await scrollExtent(page)
            expect(scrollWidth, `scrollWidth ${scrollWidth} against clientWidth ${clientWidth}`).toBeLessThanOrEqual(clientWidth)
            expect(await clippedInDialog(page), "dialog content outside the viewport").toEqual([])
          }
        })
      }
    }
  })
}

// WCAG 1.4.11: the boundary of a text field must reach 3:1 against what lies
// behind it, by its border or by its fill. Axe does not measure this. What lies
// behind a field is the stack of its ancestors' backgrounds over white.
async function weakFieldBoundaries(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const canvas = document.createElement("canvas")
    canvas.width = 1
    canvas.height = 1
    const context = canvas.getContext("2d", { willReadFrequently: true })
    if (context === null) return ["no canvas to read colours with"]
    const rgba = (css: string): number[] => {
      context.clearRect(0, 0, 1, 1)
      context.fillStyle = "#000"
      context.fillStyle = css
      context.fillRect(0, 0, 1, 1)
      const [r = 0, g = 0, b = 0, a = 0] = context.getImageData(0, 0, 1, 1).data
      return [r, g, b, a / 255]
    }
    const over = (top: number[], bottom: number[]): number[] => {
      const alpha = top[3] ?? 1
      return [0, 1, 2].map((i) => (top[i] ?? 0) * alpha + (bottom[i] ?? 0) * (1 - alpha))
    }
    const luminance = (c: number[]): number => {
      const [r = 0, g = 0, b = 0] = c.map((v) => {
        const unit = v / 255
        return unit <= 0.03928 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4
      })
      return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
    const ratio = (a: number[], b: number[]): number => {
      const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x)
      return ((light ?? 0) + 0.05) / ((dark ?? 0) + 0.05)
    }
    const backdrop = (el: Element): number[] => {
      const chain: Element[] = []
      for (let parent = el.parentElement; parent !== null; parent = parent.parentElement) chain.unshift(parent)
      return chain.reduce((behind, parent) => over(rgba(getComputedStyle(parent).backgroundColor), behind), [255, 255, 255])
    }
    const weak: string[] = []
    const fields = document.querySelectorAll(
      'input:not([type="radio"]):not([type="checkbox"]):not([type="hidden"]):not(:disabled), textarea:not(:disabled), select:not(:disabled), [role="combobox"]:not(:disabled)',
    )
    for (const el of Array.from(fields)) {
      if (el.getBoundingClientRect().width === 0) continue
      const style = getComputedStyle(el)
      const behind = backdrop(el)
      const border = Number.parseFloat(style.borderTopWidth) > 0 ? ratio(over(rgba(style.borderTopColor), behind), behind) : 1
      const fill = ratio(over(rgba(style.backgroundColor), behind), behind)
      if (Math.max(border, fill) < 3) {
        weak.push(`${el.tagName.toLowerCase()}#${el.id} border ${border.toFixed(2)}:1, fill ${fill.toFixed(2)}:1`)
      }
    }
    return weak
  })
}

test.describe("non-text contrast", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS) {
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        expect(await weakFieldBoundaries(page), "text fields whose boundary is under 3:1").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})

// WCAG 2.4.7: every control that Tab reaches must look different while it
// has focus, which darkraise-ui does with a ring (box-shadow) or an outline.
// A press that leaves the page puts focus on the body, so four presses reach
// at least one control from wherever the view left focus.
async function focusIndicators(page: Page): Promise<{ reached: number; bare: string[] }> {
  // Transitions would leave the styles read right after a blur half-way.
  await page.addStyleTag({ content: "*, *::before, *::after { transition: none !important; animation: none !important; }" })
  let reached = 0
  const bare: string[] = []
  for (let press = 0; press < 4; press++) {
    await page.keyboard.press("Tab")
    const found = await page.evaluate(() => {
      const el = document.activeElement
      if (!(el instanceof HTMLElement) || el === document.body) return null
      const look = () => {
        const style = getComputedStyle(el)
        return [style.outlineStyle, style.outlineWidth, style.outlineColor, style.boxShadow, style.borderColor].join(" ")
      }
      const focused = look()
      el.blur()
      const idle = look()
      el.focus()
      return { name: `${el.tagName.toLowerCase()} ${el.getAttribute("aria-label") ?? el.textContent?.trim().slice(0, 40) ?? ""}`, visible: focused !== idle }
    })
    if (found === null) continue
    reached++
    if (!found.visible) bare.push(found.name)
  }
  return { reached, bare }
}

test.describe("focus visible", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS) {
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        const { reached, bare } = await focusIndicators(page)
        expect(reached, "controls Tab reached").toBeGreaterThan(0)
        expect(bare, "controls without a focus indicator").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})

const NAMED = /^\s*- (button|checkbox|textbox|spinbutton|radio|combobox|link|switch|slider|searchbox)\b(.*)$/

test.describe("change dialog names", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS.filter((v) => v.dialog !== undefined)) {
      const dialogSpec = view.dialog
      if (dialogSpec === undefined) continue
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        const dialog = page.getByRole("dialog", { name: dialogSpec.title })
        await expect(dialog).toBeVisible()
        await expect(dialog).toHaveAccessibleName(dialogSpec.title)
        await expect(dialog.getByRole("heading", { name: dialogSpec.title })).toBeVisible()
        for (const [role, name] of dialogSpec.controls) {
          await expect(dialog.getByRole(role, { name })).toBeVisible()
        }
        const unnamed = (await dialog.ariaSnapshot())
          .split("\n")
          .filter((line) => {
            const found = NAMED.exec(line)
            return found !== null && !(found[2] ?? "").trimStart().startsWith('"')
          })
        expect(unnamed, "buttons, fields and links without an accessible name").toEqual([])
        await page.screenshot({ path: `${shots}${view.slug}-${theme}-names.png` })
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})
