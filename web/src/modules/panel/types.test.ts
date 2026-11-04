import { expect, test } from "vitest"
import { paExposure } from "./fixtures"
import { isStatusOutput } from "./types"

test("accepts the answer for no exposure and for a live one", () => {
  expect(isStatusOutput({ exposed: false })).toBe(true)
  expect(isStatusOutput({ exposed: false, exposure: null })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure() })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure({ via: "tunnel", tunnel: "0123abcd-0123-4abc-8def-0123456789ab", state: "needs_repair" }) })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure({ state: "revoked" }) })).toBe(true)
})

test.each([
  ["not an object", "exposed"],
  ["null", null],
  ["an array", []],
  ["exposed missing", {}],
  ["exposed not a boolean", { exposed: "yes" }],
  ["exposed without an exposure", { exposed: true }],
  ["exposed with a null exposure", { exposed: true, exposure: null }],
  ["an exposure while not exposed", { exposed: false, exposure: paExposure() }],
  ["an unknown route", { exposed: true, exposure: { ...paExposure(), via: "ssh" } }],
  ["an unknown state", { exposed: true, exposure: { ...paExposure(), state: "closed" } }],
  ["a port that is text", { exposed: true, exposure: { ...paExposure(), port: "9443" } }],
  ["a missing hostname", { exposed: true, exposure: { ...paExposure(), hostname: undefined } }],
  ["a tunnel that is not text", { exposed: true, exposure: { ...paExposure(), tunnel: 5 } }],
])("rejects %s", (_, value) => {
  expect(isStatusOutput(value)).toBe(false)
})
