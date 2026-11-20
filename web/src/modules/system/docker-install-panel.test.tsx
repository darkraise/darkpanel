import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "@/modules/firewall/fixtures"
import { DockerInstallPanel } from "./docker-install-panel"
import { PendingDockerInstall } from "./install-confirmation"
import { stepAt } from "./install-steps"

const R = "GET /api/v1/hosts/h1/resources/system/docker"
const NO_CHANGES = { "GET /api/v1/changes": { body: { changes: [] } } }
const sys = { id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

test.each([
  [{ state: "absent" }, "Install Docker"],
  [{ state: "distro", conflicts: ["docker.io", "runc"] }, "docker.io"],
  [{ state: "docker-ce", version: "27.3", running: false }, "Daemon stopped"],
])("UX-8: %o shows %s", async (docker, text) => {
  stubFetch({ ...NO_CHANGES, [R]: { body: docker } })
  renderWithProviders(<DockerInstallPanel host={makeHost({ id: "h1", modules: [sys] })} reason="daemon not running" />)
  expect((await screen.findAllByText(text)).length).toBe(docker.state === "docker-ce" ? 2 : 1)
  if (docker.state === "distro") expect(screen.queryByRole("button", { name: "Install Docker" })).toBeNull()
})

test("UX-5: Install Docker sends the firewall flag and asks for the 15-minute window", async () => {
  const calls = stubFetch({
    ...NO_CHANGES,
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
  stubFetch({ ...NO_CHANGES, [R]: { body: { state: "absent" } } })
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
  ["a finished check", [step(0, "system.docker.check/v1", "read"), step(1, "system.docker.engine-install/v1", "pending")], false, "packages"],
  ["a firewall refusal", [step(0, "system.docker.check/v1", "read"), step(1, "system.docker.engine-install/v1", "undone"), step(2, "firewall.docker.enable/v1", "undone", { code: "firewall.docker_refused", message: "UFW manages FORWARD" })], true, "firewall"],
  ["a failed package install", [step(0, "system.docker.check/v1", "read"), step(1, "system.docker.engine-install/v1", "undone"), step(2, "firewall.docker.enable/v1", "pending")], true, "packages"],
  ["no steps read yet", undefined, true, "check"],
])("UX-8: %s puts the stepper at its step", (_, steps, failed, want) => {
  expect(stepAt(ALL, steps, false, failed)).toBe(want)
})

const installStep = (ordinal: number, action: string, state: string, host = "h1") => ({
  id: `s${ordinal}`, ordinal, host, action: `${action}/v1`, state, attempt: 1, childChangeId: `k${ordinal}`, earlierChildChangeIds: null, childDeadline: null, error: null,
})
const afterReload = (state: string, host: string) => {
  const install = makeChange("system.docker.install", null, { id: "x9", state, updatedAt: new Date().toISOString() })
  return {
    [R]: { body: { state: "absent" } },
    "GET /api/v1/hosts/h1/resources/firewall/status": { body: fwStatus() },
    "GET /api/v1/changes": { body: { changes: [makeChange("core.token.create", null, { id: "c0" }), install] } },
    "GET /api/v1/changes/x9": { body: install },
    "GET /api/v1/changes/x9/steps": {
      body: { steps: [installStep(0, "system.docker.check", "read", ""), installStep(1, "system.docker.engine-install", "confirmed", host), installStep(2, "firewall.docker.enable", "applied", host)] },
    },
  }
}
const withFirewall = makeHost({ id: "h1", modules: [sys, { id: "firewall", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })

test("UX-8: after a reload the panel finds the host's install awaiting confirmation and offers Keep and Revert", async () => {
  stubFetch(afterReload("awaiting_confirmation", "h1"))
  renderWithProviders(<DockerInstallPanel host={withFirewall} reason="docker absent" />)
  await screen.findByTestId("state-awaitingConfirm")
  screen.getByRole("button", { name: "Keep" })
  screen.getByRole("button", { name: "Revert" })
})

test("UX-8: after a reload a rollback within the day reads Install rolled back; another host's install is ignored", async () => {
  stubFetch(afterReload("reverted", "h1"))
  const { unmount } = renderWithProviders(<DockerInstallPanel host={withFirewall} reason="docker absent" />)
  await screen.findByText("Install rolled back")
  unmount()
  stubFetch(afterReload("awaiting_confirmation", "h2"))
  renderWithProviders(<DockerInstallPanel host={withFirewall} reason="docker absent" />)
  await screen.findByTestId("state-dockerAbsent")
  expect(screen.queryByRole("button", { name: "Keep" })).toBeNull()
})

test("UX-5: the Docker page keeps an install's Keep and Revert once Docker is available", async () => {
  stubFetch(afterReload("awaiting_confirmation", "h1"))
  const { unmount } = renderWithProviders(<PendingDockerInstall hostId="h1" />)
  const region = within(await screen.findByRole("region", { name: "Docker install awaiting confirmation" }))
  region.getByRole("button", { name: "Keep" })
  unmount()
  stubFetch({ "GET /api/v1/changes": { body: { changes: [] } } })
  const { container } = renderWithProviders(<PendingDockerInstall hostId="h1" />)
  await new Promise((r) => setTimeout(r, 50))
  expect(container.querySelector("[data-testid='state-pendingInstall']")).toBeNull()
})
