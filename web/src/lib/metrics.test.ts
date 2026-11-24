import { describe, expect, test } from "vitest"
import { thinSeries, type Series } from "./metrics"

const s = (key: string, values: (number | null)[]): Series => ({ key, label: key, values })

describe("thinSeries", () => {
  test("a range within maxPoints is drawn as it is", () => {
    const times = [0, 60, 120]
    const series = [s("cpu", [1, null, 3])]
    expect(thinSeries(times, series, 3)).toEqual({ times, series })
  })

  test("each bucket keeps its min and max in time order, at the bucket's first and last time", () => {
    const times = [0, 1, 2, 3, 4, 5, 6, 7]
    const got = thinSeries(times, [s("a", [5, 9, 1, 3, 2, 2, 8, 0]), s("b", [4, 0, 1, 7, 6, 6, 6, 6])], 4)
    expect(got.times).toEqual([0, 3, 4, 7])
    expect(got.series).toEqual([s("a", [9, 1, 8, 0]), s("b", [0, 7, 6, 6])])
  })

  test("an all-null bucket stays a break, and a partly null bucket keeps its samples", () => {
    const times = [0, 1, 2, 3, 4, 5]
    const got = thinSeries(times, [s("a", [null, null, null, 4, null, 2])], 4)
    expect(got.series[0]!.values).toEqual([null, null, 4, 2])
  })

  test("a one-slot last bucket is one row", () => {
    const got = thinSeries([0, 1, 2, 3, 4, 5, 6], [s("a", [1, 2, 3, 4, 5, 6, 7])], 6)
    expect(got.times).toEqual([0, 2, 3, 5, 6])
    expect(got.series[0]!.values).toEqual([1, 3, 4, 6, 7])
  })

  test("7 days of minutes fit the default budget and keep every spike", () => {
    const n = 7 * 1440
    const times = Array.from({ length: n }, (_, i) => i * 60_000)
    const values = times.map((_, i) => (i === 4321 ? 99 : i === 777 ? -5 : 50))
    const got = thinSeries(times, [s("cpu", values)])
    expect(got.times.length).toBeLessThanOrEqual(2400)
    expect(got.times[0]).toBe(0)
    expect(got.times[got.times.length - 1]).toBe(times[n - 1])
    expect(Math.max(...(got.series[0]!.values as number[]))).toBe(99)
    expect(Math.min(...(got.series[0]!.values as number[]))).toBe(-5)
  })
})
