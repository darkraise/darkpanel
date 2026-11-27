import { useState } from "react"
import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { useReopenFocus } from "./return-focus"

function Form() {
  const [hidden, setHidden] = useState(false)
  const submit = useReopenFocus(hidden)
  return (
    <>
      <Dialog open={!hidden}>
        <DialogContent>
          <DialogTitle>Add a user</DialogTitle>
          <Input aria-label="Name" />
          <Button ref={submit} onClick={() => setHidden(true)}>
            Review change
          </Button>
        </DialogContent>
      </Dialog>
      {hidden && <Button onClick={() => setHidden(false)}>Back</Button>}
    </>
  )
}

describe("useReopenFocus", () => {
  it("opens on the first field, and reopens on the submit button", async () => {
    const user = userEvent.setup()
    render(<Form />)
    expect(document.activeElement).toBe(screen.getByLabelText("Name"))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await user.click(screen.getByRole("button", { name: "Back" }))
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Review change" }))
  })
})
