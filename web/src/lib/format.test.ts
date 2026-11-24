import { describe, expect, test } from "vitest"
import { formatBytes, formatDuration, formatTime, formatUptime } from "./format"

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

  test("formats this year's timestamp as day and 24-hour time in the browser's locale", () => {
    const iso = `${new Date().getFullYear()}-10-19T08:30:00Z`
    const want = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(iso))
    expect(formatTime(iso)).toBe(want)
    expect(formatTime(iso)).toMatch(/\d{2}:\d{2}$/)
    expect(formatTime(iso)).not.toContain(String(new Date().getFullYear()))
  })

  test("names the year of a timestamp from another year", () => {
    const iso = "2020-10-19T08:30:00Z"
    const want = new Intl.DateTimeFormat(undefined, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(iso))
    expect(formatTime(iso)).toBe(want)
    expect(formatTime(iso)).toContain("2020")
  })
})

describe("formatUptime", () => {
  test.each([
    [59, "59 s"],
    [3_660, "1 h 1 min"],
    [3_553_200, "41 d 3 h"],
    [90_061, "1 d 1 h"],
  ])("%d seconds read %s", (seconds, want) => {
    expect(formatUptime(seconds)).toBe(want)
  })
})
