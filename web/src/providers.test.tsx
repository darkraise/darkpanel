import { act, render } from "@testing-library/react"
import { QueryClient } from "@tanstack/react-query"
import { ACCENT_COLORS, useTheme } from "darkraise-ui/theme"
import { afterEach, expect, test } from "vitest"
import { Providers } from "./providers"

afterEach(() => {
  localStorage.clear()
  delete document.documentElement.dataset.accent
  delete document.documentElement.dataset.surface
})

test("Providers mirrors the theme's accent and surface onto <html>", () => {
  let setAccentColor: ReturnType<typeof useTheme>["setAccentColor"] = () => {}
  function Probe() {
    setAccentColor = useTheme().setAccentColor
    return null
  }

  render(
    <Providers client={new QueryClient()}>
      <Probe />
    </Providers>,
  )

  expect(document.documentElement.dataset.accent).toBe("blue")
  expect(document.documentElement.dataset.surface).toBe("zinc")

  const other = ACCENT_COLORS.find((c) => c !== "blue")
  if (!other) throw new Error("darkraise-ui offers no accent besides blue")
  act(() => setAccentColor(other))

  expect(document.documentElement.dataset.accent).toBe(other)
})
