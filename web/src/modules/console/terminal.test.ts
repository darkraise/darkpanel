import { expect, test } from "vitest"
import { closedFor } from "./terminal"

test("error close reason", () => {
  expect(closedFor("error", null)).toEqual({ text: "The panel couldn't check your session", label: "Closed", reconnect: true })
})
