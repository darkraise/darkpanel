import { useState } from "react"
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { SystemChange, type PendingChange } from "./system-change"

// The row's Remove button is gone once its dialog closes, as after a remove.
function Row() {
  const [pending, setPending] = useState<PendingChange | null>(null)
  const [gone, setGone] = useState(false)
  return (
    <section aria-label="Installed">
      {!gone && (
        <button type="button" onClick={() => setPending({ action: "system.packages.remove", title: "Remove jq", input: { names: ["jq"], purge: false } })}>
          Remove jq
        </button>
      )}
      <SystemChange
        hostId="h1"
        pending={pending}
        onClose={() => {
          setPending(null)
          setGone(true)
        }}
      />
    </section>
  )
}

test("U7: when the trigger is gone after the dialog closes, focus goes to its section, not the page", async () => {
  stubFetch({ "POST /api/v1/hosts/h1/actions/system.packages.remove/v1/plan": { body: makePlan("system.packages.remove") } })
  renderWithProviders(<Row />)
  await userEvent.click(screen.getByRole("button", { name: "Remove jq" }))
  await screen.findByRole("dialog")
  await userEvent.keyboard("{Escape}")
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("region", { name: "Installed" })))
})
