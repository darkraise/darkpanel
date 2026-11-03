import { expect, test } from "vitest"
import { parseDomains, parseOptionalId, parsePort, parseRules } from "./parse"

test.each([
  ["app.example.com", ["app.example.com"]],
  ["App.Example.com, www.example.com", ["app.example.com", "www.example.com"]],
  ["  a.example.com   b.example.com,,c.example.com ", ["a.example.com", "b.example.com", "c.example.com"]],
  ["a.example.com, A.example.com", ["a.example.com"]],
  ["", []],
  [" , ", []],
])("parseDomains(%j) is %j", (text, want) => {
  expect(parseDomains(text)).toEqual(want)
})

test.each([
  ["80", 80],
  [" 8080 ", 8080],
  ["65535", 65535],
  ["0", null],
  ["65536", null],
  ["80.5", null],
  ["-1", null],
  ["1e3", null],
  ["http", null],
  ["", null],
])("parsePort(%j) is %j", (text, want) => {
  expect(parsePort(text)).toBe(want)
})

test.each([
  ["", 0],
  ["   ", 0],
  ["7", 7],
  [" 12 ", 12],
  ["0", null],
  ["-1", null],
  ["1.5", null],
  ["abc", null],
])("parseOptionalId(%j) is %j", (text, want) => {
  expect(parseOptionalId(text)).toBe(want)
})

test("parseRules reads one rule per line, ignoring blank lines and the case of the directive", () => {
  expect(parseRules("allow 10.0.0.0/8\n\n  DENY   192.0.2.1  \r\nAllow 2001:db8::/32")).toEqual({
    ok: true,
    clients: [
      { address: "10.0.0.0/8", directive: "allow" },
      { address: "192.0.2.1", directive: "deny" },
      { address: "2001:db8::/32", directive: "allow" },
    ],
  })
})

test.each([
  ["", "Add at least one rule."],
  ["\n  \n", "Add at least one rule."],
  ["permit 10.0.0.1", "Line 1: start with allow or deny, then an address."],
  ["allow 10.0.0.1\n\nallow", "Line 3: start with allow or deny, then an address."],
  ["allow 10.0.0.1 extra", "Line 1: start with allow or deny, then an address."],
])("parseRules(%j) is refused with %j", (text, error) => {
  expect(parseRules(text)).toEqual({ ok: false, error })
})
