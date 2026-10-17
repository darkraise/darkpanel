import { render, screen } from "@testing-library/react"
import { expect, test } from "vitest"
import { App } from "./app"

test("renders the Darkpanel heading", () => {
  render(<App />)
  expect(screen.getByRole("heading", { name: "Darkpanel" })).toBeDefined()
})
