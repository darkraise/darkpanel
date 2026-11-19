import { beforeEach, expect, test } from "vitest"
import { readFileSync } from "node:fs"
import config from "./theme.config"

const init = readFileSync("public/theme-init.js", "utf8")

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute("data-style")
  document.documentElement.removeAttribute("data-preset")
})

test("A3: the switcher offers only mode and accent colour", () => {
  const shown = Object.entries(config.switcher.axes ?? {})
    .filter(([, on]) => on !== false)
    .map(([key]) => key)
  expect(shown).toEqual(["mode", "accentColor"])
  expect(config.switcher.advanced).toBe(false)
  expect(config.defaults).toMatchObject({ style: "glass", preset: "darkpanel", mode: "dark", density: "compact" })
})

test("A2: the first-paint script stamps the glass style and darkpanel preset", () => {
  new Function(init)()
  expect(document.documentElement.dataset.style).toBe("glass")
  expect(document.documentElement.dataset.preset).toBe("darkpanel")
})

test("A4: a 6.x browser keeps mode and accent, loses every other theme key once", () => {
  localStorage.setItem("mode", "light")
  localStorage.setItem("theme-accent", "violet")
  localStorage.setItem("theme-preset", "aurora")
  localStorage.setItem("theme-radius", "lg")
  localStorage.setItem("theme-control-depth", "raised")
  new Function(init)()
  expect(localStorage.getItem("mode")).toBe("light")
  expect(localStorage.getItem("theme-accent")).toBe("violet")
  expect(localStorage.getItem("theme-preset")).toBeNull()
  expect(localStorage.getItem("theme-radius")).toBeNull()
  expect(localStorage.getItem("theme-control-depth")).toBeNull()
  expect(localStorage.getItem("darkpanel-theme-v2")).not.toBeNull()
  localStorage.setItem("theme-radius", "sm")
  new Function(init)()
  expect(localStorage.getItem("theme-radius")).toBe("sm")
})
