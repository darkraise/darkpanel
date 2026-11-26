import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import { Button } from "darkraise-ui/components/button"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { useStepUp } from "./step-up-dialog"

const STEP_UP = "POST /api/v1/auth/step-up"
const REASON = "Opening a terminal needs your authenticator code"

function problem(status: number, code: string, detail: string, headers: Record<string, string> = {}): StubResponse {
  return { status, body: { type: "about:blank", title: "", status, detail, code }, headers }
}

// Trigger opens the shared dialog from a button, as a page does, and shows
// how the step-up ended.
function Trigger({ submit }: { submit?: (code: string) => Promise<void> }) {
  const { stepUp } = useStepUp()
  return (
    <>
      <Button
        onClick={() => {
          void stepUp(REASON, submit).then((ok) => {
            document.body.dataset.stepUp = ok ? "verified" : "cancelled"
          })
        }}
      >
        Open terminal
      </Button>
    </>
  )
}

async function open(submit?: (code: string) => Promise<void>) {
  const user = userEvent.setup()
  renderWithProviders(<Trigger submit={submit} />)
  await user.click(screen.getByRole("button", { name: "Open terminal" }))
  const dialog = await screen.findByRole("dialog")
  return { user, dialog }
}

afterEach(() => {
  delete document.body.dataset.stepUp
  vi.unstubAllGlobals()
})

describe("StepUpDialog", () => {
  test("opens with focus and submits on Enter", async () => {
    const calls = stubFetch({ [STEP_UP]: { status: 204 } })
    const { user, dialog } = await open()
    expect(within(dialog).getByText(REASON)).toBeTruthy()
    expect(within(dialog).getByRole("button", { name: "Verify" })).toBeTruthy()
    await waitFor(() => expect(focusedName()).toBe("Authenticator code"))
    await user.keyboard("123456{Enter}")
    await waitFor(() => expect(document.body.dataset.stepUp).toBe("verified"))
    expect(calls.filter((c) => c.path === "/api/v1/auth/step-up").map((c) => c.body)).toEqual([{ code: "123456" }])
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  test.each([
    ["a wrong code", problem(401, "auth.invalid_credentials", "wrong authenticator code"), "That code didn't work"],
    ["a code of the step already used", problem(409, "auth.step_up_wait", "Wait for the next code"), "Wait for the next code"],
    ["the rate limit", problem(429, "auth.rate_limited", "too many", { "Retry-After": "30" }), "Too many attempts; try again in 30 s"],
  ])("shows each error: %s", async (_name, answer, text) => {
    stubFetch({ [STEP_UP]: answer })
    const { user, dialog } = await open()
    await user.keyboard("654321{Enter}")
    expect(await within(dialog).findByText(text)).toBeTruthy()
    expect(focusedName()).toBe("Authenticator code")
    expect(document.body.dataset.stepUp).toBeUndefined()
    await expectNoAxeViolations(dialog)
  })

  test("shows each error: no confirmed authenticator replaces the field", async () => {
    stubFetch({ [STEP_UP]: problem(403, "auth.step_up_unavailable", "Set up an authenticator to use the console and jobs") })
    const { user, dialog } = await open()
    await user.keyboard("654321{Enter}")
    expect(await within(dialog).findByText("Set up an authenticator to use the console and jobs")).toBeTruthy()
    expect(within(dialog).queryByLabelText("Authenticator code")).toBeNull()
    expect(within(dialog).queryByRole("button", { name: "Verify" })).toBeNull()
    await expectNoAxeViolations(dialog)
  })

  test("shows each error: Escape closes and returns focus to the trigger", async () => {
    const calls = stubFetch({})
    const { user, dialog } = await open()
    await expectNoAxeViolations(dialog)
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(focusedName()).toBe("Open terminal"))
    await waitFor(() => expect(document.body.dataset.stepUp).toBe("cancelled"))
    expect(calls).toEqual([])
  })

  test("a caller's submit receives the code in place of the step-up endpoint", async () => {
    const calls = stubFetch({})
    const submit = vi.fn<(code: string) => Promise<void>>().mockResolvedValue(undefined)
    const { user } = await open(submit)
    await user.keyboard("111222{Enter}")
    await waitFor(() => expect(document.body.dataset.stepUp).toBe("verified"))
    expect(submit).toHaveBeenCalledExactlyOnceWith("111222")
    expect(calls).toEqual([])
  })
})
