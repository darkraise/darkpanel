import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "@/modules/firewall/fixtures"
import { DockerInstallPanel } from "./docker-install-panel"
import { stepAt } from "./install-steps"

const R = "GET /api/v1/hosts/h1/resources/system/docker"
const sys = { id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

test.each([
  [{ state: "absent" }, "Install Docker"],
  [{ state: "distro", conflicts: ["docker.io", "runc"] }, "docker.io"],
  [{ state: "docker-ce", version: "27.3", running: false }, "Daemon stopped"],
])("UX-8: %o shows %s", async (docker, text) => {
  stubFetch({ [R]: { body: docker } })
  renderWithProviders(<DockerInstallPanel host={makeHost({ id: "h1", modules: [sys] })} reason="daemon not running" />)
  expect(await screen.findByText(text)).toBeTruthy()
  if (docker.state === "distro") expect(screen.queryByRole("button", { name: "Install Docker" })).toBeNull()
})

test("UX-5: Install Docker sends the firewall flag and asks for the 15-minute window", async () => {
  const calls = stubFetch({
    [R]: { body: { state: "absent" } },
    "GET /api/v1/hosts/h1/resources/firewall/status": { body: fwStatus() },
    "POST /api/v1/actions/system.docker.install/v1/plan": { body: makePlan("system.docker.install", { hostId: "h1" }) },
    "POST /api/v1/actions/system.docker.install/v1/apply": { status: 202, body: makeChange("system.docker.install/v1", null, { state: "applying" }) },
  })
  renderWithProviders(<DockerInstallPanel host={makeHost({ id: "h1", modules: [sys, { id: "firewall", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} reason="docker absent" />)
  await userEvent.click(await screen.findByRole("button", { name: "Install Docker" }))
  expect((calls.find((c) => c.path.endsWith("/plan"))?.body as { input: unknown }).input).toEqual({ hostId: "h1", firewallIntegration: true })
  await userEvent.click(await screen.findByRole("button", { name: "Apply" }))
  const apply = calls.find((c) => c.path.endsWith("/apply"))?.body as { confirmTimeoutSeconds?: number }
  expect(apply.confirmTimeoutSeconds).toBe(900)
})

test("UX-8: a rolled-back install says so and offers install again; agent too old asks for an update", async () => {
  stubFetch({ [R]: { body: { state: "absent" } } })
  const { unmount } = renderWithProviders(<DockerInstallPanel host={makeHost({ id: "h1", modules: [sys] })} reason="docker absent" lastInstall={{ state: "reverted" }} />)
  await screen.findByText("Install rolled back")
  screen.getByRole("button", { name: "Install Docker" })
  unmount()
  renderWithProviders(<DockerInstallPanel host={makeHost({ id: "h1", modules: [] })} reason="docker absent" />)
  screen.getByText("Agent update needed")
})

const step = (ordinal: number, action: string, state: string, error: { code: string; message: string } | null = null) => ({
  id: `s${ordinal}`, ordinal, host: "h1", action, state, attempt: 1, childChangeId: `k${ordinal}`, earlierChildChangeIds: null, childDeadline: null, error,
})
const ALL = ["check", "packages", "firewall", "confirm"].map((id) => ({ id }))

test.each([
  ["a running install", [step(0, "system.docker.check/v1", "read"), step(1, "system.docker.engine-install/v1", "applying")], false, "packages"],
  ["a finished check", [step(0, "system.docker.check", "read"), step(1, "system.docker.engine-install", "pending")], false, "packages"],
  ["a firewall refusal", [step(0, "system.docker.check/v1", "read"), step(1, "system.docker.engine-install/v1", "undone"), step(2, "firewall.docker.enable/v1", "undone", { code: "firewall.docker_refused", message: "UFW manages FORWARD" })], true, "firewall"],
  ["a failed package install", [step(0, "system.docker.check", "read"), step(1, "system.docker.engine-install", "undone"), step(2, "firewall.docker.enable", "pending")], true, "packages"],
  ["no steps read yet", undefined, true, "check"],
])("UX-8: %s puts the stepper at its step", (_, steps, failed, want) => {
  expect(stepAt(ALL, steps, false, failed)).toBe(want)
})
