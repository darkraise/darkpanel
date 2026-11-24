import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { SignInSettings } from "@/api/types"
import { makeChange, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { SignInSettingsPage } from "./sign-in-settings"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

const configured: SignInSettings = {
  oidc: { issuer: "https://login.example.com/realms/ops", clientId: "darkpanel", displayName: "Acme SSO", redirectBase: "https://panel.example.com", requireTotp: true, clientSecretSet: true },
  tailscale: { enabled: false, requireTotp: false },
}
const off: SignInSettings = { oidc: null, tailscale: { enabled: true, requireTotp: true } }

function stub(settings: SignInSettings, extra: Parameters<typeof stubFetch>[0] = {}, bound = true) {
  return stubFetch({
    [route("auth.settings.get", "read")]: { body: { output: settings } },
    "GET /api/v1/controller/listeners": { body: { tailscaleBound: bound, message: "" } },
    ...extra,
  })
}

const card = async (name: string) => screen.findByRole("region", { name })

test("UI-3 a configured OIDC card shows its fields, the code switch and a saved secret", async () => {
  const calls = stub(configured, {
    [route("auth.oidc.set", "plan")]: { body: makePlan("auth.oidc.set", { confirmation: null }) },
    [route("auth.oidc.clear", "plan")]: { body: makePlan("auth.oidc.clear", { confirmation: null }) },
  })
  renderWithProviders(<SignInSettingsPage />)
  const user = userEvent.setup()
  const oidc = await card("OIDC")
  expect(within(oidc).getByText("Configured")).toBeTruthy()
  expect((within(oidc).getByLabelText("Issuer URL") as HTMLInputElement).value).toBe("https://login.example.com/realms/ops")
  expect((within(oidc).getByLabelText("Client ID") as HTMLInputElement).value).toBe("darkpanel")
  const secret = within(oidc).getByLabelText("Client secret") as HTMLInputElement
  expect(secret.value).toBe("")
  expect(secret.type).toBe("password")
  expect(within(oidc).getByText("Saved — enter a new value to replace it.")).toBeTruthy()
  expect(within(oidc).getByText('Shown on the login page as "Sign in with Acme SSO".')).toBeTruthy()
  expect(within(oidc).getByRole("switch", { name: "Require Darkpanel code too" }).getAttribute("aria-checked")).toBe("true")

  await user.click(within(oidc).getByRole("button", { name: "Save" }))
  await screen.findByRole("dialog", { name: "Save the OIDC settings" })
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("auth.oidc.set/v1/plan"))?.body).toEqual({
      input: { issuer: "https://login.example.com/realms/ops", clientId: "darkpanel", displayName: "Acme SSO", redirectBase: "https://panel.example.com", requireTotp: true },
    }),
  )
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

  await user.click(within(oidc).getByRole("button", { name: "Turn off" }))
  await screen.findByRole("dialog", { name: "Turn off OIDC sign-in" })
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("auth.oidc.clear/v1/plan"))?.body).toEqual({ input: {} }))
})

test("an OIDC card that is off asks for every field, the secret included", async () => {
  const calls = stub(off, {
    [route("auth.oidc.set", "plan")]: { body: makePlan("auth.oidc.set", { confirmation: null }) },
  })
  renderWithProviders(<SignInSettingsPage />)
  const user = userEvent.setup()
  const oidc = await card("OIDC")
  expect(within(oidc).getByText("Off")).toBeTruthy()
  expect(within(oidc).queryByRole("button", { name: "Turn off" })).toBeNull()
  expect(within(oidc).queryByText(/^Saved/)).toBeNull()
  await user.click(within(oidc).getByRole("button", { name: "Save" }))
  expect(within(oidc).getByText("Enter an https URL, such as https://login.example.com.")).toBeTruthy()
  expect(within(oidc).getByText("Enter the client ID the provider issued.")).toBeTruthy()
  expect(within(oidc).getByText("Enter the client secret. None is saved yet.")).toBeTruthy()
  expect(within(oidc).getByText("Use 1 to 32 characters.")).toBeTruthy()
  expect(within(oidc).getByText("Enter the https address users open Darkpanel at.")).toBeTruthy()
  expect(within(oidc).getByLabelText("Issuer URL").getAttribute("aria-invalid")).toBe("true")
  expect(calls.some((c) => c.path.endsWith("auth.oidc.set/v1/plan"))).toBe(false)

  await user.type(within(oidc).getByLabelText("Issuer URL"), "http://login.example.com")
  await user.type(within(oidc).getByLabelText("Client ID"), "dp")
  await user.type(within(oidc).getByLabelText("Client secret"), "s3cret")
  await user.type(within(oidc).getByLabelText("Button name"), "Acme SSO")
  await user.type(within(oidc).getByLabelText("Redirect base"), "https://panel.example.com/some/path")
  await user.click(within(oidc).getByRole("button", { name: "Save" }))
  expect(within(oidc).getByText("Enter an https URL, such as https://login.example.com.")).toBeTruthy()
  await user.clear(within(oidc).getByLabelText("Issuer URL"))
  await user.type(within(oidc).getByLabelText("Issuer URL"), "https://login.example.com")
  await user.click(within(oidc).getByRole("button", { name: "Save" }))
  await screen.findByRole("dialog", { name: "Save the OIDC settings" })
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("auth.oidc.set/v1/plan"))?.body).toEqual({
      input: { issuer: "https://login.example.com", clientId: "dp", clientSecret: "s3cret", displayName: "Acme SSO", redirectBase: "https://panel.example.com", requireTotp: false },
    }),
  )
})

test("UI-3 UI-5 the Tailscale card shows two switches and the tailnet-address note", async () => {
  const calls = stub(
    configured,
    {
      [route("auth.tailscale.set", "plan")]: { body: makePlan("auth.tailscale.set", { confirmation: null }) },
      [route("auth.tailscale.set", "apply")]: { body: makeChange("auth.tailscale.set", { ...configured, tailscale: { enabled: true, requireTotp: false } }) },
    },
    false,
  )
  renderWithProviders(<SignInSettingsPage />)
  const user = userEvent.setup()
  const ts = await card("Tailscale identity")
  expect(within(ts).getByText("Off")).toBeTruthy()
  expect(within(ts).getByText("Works only when Darkpanel is opened at its tailnet address.")).toBeTruthy()
  expect(await within(ts).findByText("Darkpanel is not listening on the tailnet now.")).toBeTruthy()
  const on = within(ts).getByRole("switch", { name: "Sign in with Tailscale identity" })
  const totp = within(ts).getByRole("switch", { name: "Require the code every time" })
  expect(totp.getAttribute("aria-describedby")).toBeTruthy()
  expect(document.getElementById(totp.getAttribute("aria-describedby") ?? "")?.textContent).toBe("When off, each device asks for the code once and is then remembered.")
  expect(on.getAttribute("aria-checked")).toBe("false")
  expect(totp.getAttribute("aria-checked")).toBe("false")
  await user.click(on)
  await user.click(within(ts).getByRole("button", { name: "Save" }))
  const change = await screen.findByRole("dialog", { name: "Save the Tailscale sign-in settings" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  expect(calls.find((c) => c.path.endsWith("auth.tailscale.set/v1/plan"))?.body).toEqual({ input: { enabled: true, requireTotp: false } })
})

test("a failed load says so and retries", async () => {
  let fail = true
  stubFetch({
    [route("auth.settings.get", "read")]: () =>
      fail ? { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } } : { body: { output: off } },
    "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "" } },
  })
  renderWithProviders(<SignInSettingsPage />)
  expect(await screen.findByText("Couldn't load the sign-in settings: boom")).toBeTruthy()
  fail = false
  await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
  expect(await card("Tailscale identity")).toBeTruthy()
  expect(screen.queryByText("Darkpanel is not listening on the tailnet now.")).toBeNull()
})
