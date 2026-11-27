import { expect, test } from "vitest"
import { closedFor } from "./terminal"

test("error close reason", () => {
  expect(closedFor("error", null)).toEqual({ text: "The panel couldn't check your session", label: "Closed", reconnect: true })
})

test("bad frame and browser gone close reasons", () => {
  expect(closedFor("bad frame", null)).toEqual({ text: "The terminal closed: a message from the browser was too large or malformed", label: "Closed", reconnect: true })
  expect(closedFor("browser gone", null)).toEqual({ text: "The connection stopped answering", label: "Closed: connection lost", reconnect: true })
})
