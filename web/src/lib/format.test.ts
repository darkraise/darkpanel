import { describe, expect, test } from "vitest"
import { formatBytes, formatDuration, formatTime } from "./format"

describe("formatBytes", () => {
  test.each([
    [0, "0 B"],
    [512, "512 B"],
    [1536, "1.5 KiB"],
    [8589934592, "8.0 GiB"],
    [107374182400, "100 GiB"],
    [-1, "—"],
    [Number.NaN, "—"],
  ])("%d bytes is %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want)
  })
})

describe("formatDuration", () => {
  test.each([
    [273900, "3 d 4 h 5 min"],
    [14700, "4 h 5 min"],
    [300, "5 min"],
    [42.9, "42 s"],
    [-5, "—"],
  ])("%d seconds is %s", (seconds, want) => {
    expect(formatDuration(seconds)).toBe(want)
  })
})

describe("formatTime", () => {
  test("shows a dash for null and keeps an unparsable value", () => {
    expect(formatTime(null)).toBe("—")
    expect(formatTime("not a time")).toBe("not a time")
  })

  test("formats a timestamp in the browser's locale", () => {
    const iso = "2026-10-19T08:30:00Z"
    const want = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso))
    expect(formatTime(iso)).toBe(want)
  })
})
