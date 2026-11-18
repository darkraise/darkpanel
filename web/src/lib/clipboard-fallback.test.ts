import { afterEach, expect, test, vi } from "vitest"
import { installClipboardFallback } from "./clipboard-fallback"

const original = Object.getOwnPropertyDescriptor(navigator, "clipboard")

afterEach(() => {
  if (original) Object.defineProperty(navigator, "clipboard", original)
  else Reflect.deleteProperty(navigator, "clipboard")
  vi.restoreAllMocks()
})

function removeClipboard() {
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
}

test("copies through execCommand when the clipboard API is missing", async () => {
  removeClipboard()
  let copied = ""
  document.execCommand = vi.fn((cmd: string) => {
    copied = cmd === "copy" ? (document.activeElement as HTMLTextAreaElement).value : ""
    return true
  })
  installClipboardFallback()
  await navigator.clipboard.writeText("dpt_secret")
  expect(copied).toBe("dpt_secret")
  expect(document.querySelector("textarea")).toBeNull()
})

test("rejects when execCommand cannot copy", async () => {
  removeClipboard()
  document.execCommand = vi.fn(() => false)
  installClipboardFallback()
  await expect(navigator.clipboard.writeText("x")).rejects.toThrow("copy is not available")
})

test("leaves a present clipboard API alone", () => {
  const writeText = vi.fn(async () => {})
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
  installClipboardFallback()
  expect(navigator.clipboard.writeText).toBe(writeText)
})
