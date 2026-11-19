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

test("selects the field that shows the text and gives focus back", async () => {
  removeClipboard()
  const field = document.createElement("input")
  field.value = "dpt_shown"
  field.readOnly = true
  const button = document.createElement("button")
  document.body.append(field, button)
  button.focus()
  let selected = ""
  document.execCommand = vi.fn(() => {
    const el = document.activeElement as HTMLInputElement
    selected = el === field ? el.value.slice(el.selectionStart ?? 0, el.selectionEnd ?? 0) : ""
    return true
  })
  installClipboardFallback()
  await navigator.clipboard.writeText("dpt_shown")
  expect(selected).toBe("dpt_shown")
  expect(document.activeElement).toBe(button)
  expect(document.querySelector("textarea")).toBeNull()
  field.remove()
  button.remove()
})

test("selects the field in the open dialog, not an earlier one on the page", async () => {
  removeClipboard()
  const behind = document.createElement("input")
  behind.value = "dpt_shown"
  const dialog = document.createElement("div")
  dialog.setAttribute("role", "dialog")
  const field = document.createElement("input")
  field.value = "dpt_shown"
  field.readOnly = true
  const button = document.createElement("button")
  dialog.append(field, button)
  document.body.append(behind, dialog)
  button.focus()
  let from: Element | null = null
  document.execCommand = vi.fn(() => {
    from = document.activeElement
    return true
  })
  installClipboardFallback()
  await navigator.clipboard.writeText("dpt_shown")
  expect(from).toBe(field)
  behind.remove()
  dialog.remove()
})
