import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { DockerPanel } from "./docker-panel"
import { fwDocker } from "./fixtures"
import type { DockerStatus } from "./types"

vi.mock("./docker-sources", () => ({
  DockerSources: ({ hostId, docker }: { hostId: string; docker: { settings: { privateSources: string[] } } }) => (
    <div data-testid="docker-sources">{`${hostId}: ${docker.settings.privateSources.join(", ")}`}</div>
  ),
}))

const DOCKER = "GET /api/v1/hosts/h1/resources/firewall/docker"

// dpDocker is a supported Docker host with the integration off; every field
// a test reads is set here, so the tests do not depend on the fixture's.
function dpDocker(over: Partial<DockerStatus> = {}): DockerStatus {
  return fwDocker({
    available: true,
    version: "29.9.1",
    driver: "iptables",
    dockerVariant: "nft",
    ufwVariant: "nft",
    rawProtection: true,
    supported: true,
    unsupported: [],
    enabled: false,
    health: "off",
    issues: [],
    foreignRules: [],
    manualRoutes: [],
    networks: [
      { name: "bridge", id: "b1", bridge: "docker0", subnets: ["172.17.0.0/16"], protected: true },
      { name: "public", id: "n2", bridge: "dppublic", subnets: ["203.0.113.0/24"], protected: false, reason: "custom bridge name with a non-private subnet" },
    ],
    settings: { privateSources: ["10.0.0.0/8", "100.64.0.0/10"], forwardExemptions: [] },
    ...over,
  })
}

function dpRender(docker: DockerStatus, routes: Record<string, StubHandler> = {}): { calls: FetchCall[]; container: HTMLElement } {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
  }
  const calls = stubFetch({
    [DOCKER]: { body: docker },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.docker.enable/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.docker.disable/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.docker.repair/v1/plan": plan,
    ...routes,
  })
  const { container } = renderWithProviders(<DockerPanel hostId="h1" />)
  return { calls, container }
}

async function dpPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("an off, supported integration shows what was detected and plans docker.enable", async () => {
  const { calls } = dpRender(dpDocker())
  const detection = await screen.findByLabelText("Docker detection")
  const rows = Array.from(detection.querySelectorAll("dt")).map((dt) => [dt.textContent, dt.nextElementSibling?.textContent])
  expect(rows).toEqual([
    ["Docker", "29.9.1"],
    ["Firewall backend", "iptables"],
    ["Docker chains", "nft"],
    ["UFW chains", "nft"],
    ["Raw-table protection", "Loaded"],
    ["Supported", "Yes"],
  ])
  expect(screen.getByText("off").textContent).toBe("off")
  expect(screen.queryByRole("button", { name: "Disable integration" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Enable integration" }))
  expect(await dpPlanned(calls, "Enable the Docker integration")).toEqual({ path: "/api/v1/hosts/h1/actions/firewall.docker.enable/v1/plan", body: { input: {} } })
})

test("an unsupported combination lists its reasons and cannot be enabled", async () => {
  dpRender(dpDocker({ supported: false, health: "unsupported", unsupported: ["Docker's chains are in the legacy variant and UFW's in nft", "Docker's raw protection is not loaded"] }))
  expect((await screen.findByText("Unsupported Docker configuration")).textContent).toBe("Unsupported Docker configuration")
  const reasons = within(screen.getByRole("list", { name: "Unsupported Docker reasons" })).getAllByRole("listitem")
  expect(reasons.map((li) => li.textContent)).toEqual(["Docker's chains are in the legacy variant and UFW's in nft", "Docker's raw protection is not loaded"])
  expect(screen.getByRole("button", { name: "Enable integration" }).hasAttribute("disabled")).toBe(true)
})

test("a healthy integration offers Disable and plans docker.disable", async () => {
  const { calls } = dpRender(dpDocker({ enabled: true, health: "ok" }))
  await userEvent.setup().click(await screen.findByRole("button", { name: "Disable integration" }))
  expect(screen.queryByRole("button", { name: "Enable integration" })).toBeNull()
  expect(screen.queryByRole("button", { name: "Repair" })).toBeNull()
  expect(await dpPlanned(calls, "Disable the Docker integration")).toEqual({ path: "/api/v1/hosts/h1/actions/firewall.docker.disable/v1/plan", body: { input: {} } })
})

test("a broken integration lists its issues and plans docker.repair", async () => {
  const { calls } = dpRender(
    dpDocker({
      enabled: true,
      health: "broken",
      issues: [
        { level: "broken", reason: "the block is missing from /etc/ufw/after.rules" },
        { level: "degraded", reason: "network public is not protected" },
      ],
    }),
  )
  const issues = within(await screen.findByRole("list", { name: "Docker health issues" })).getAllByRole("listitem")
  expect(issues.map((li) => li.textContent)).toEqual(["brokenthe block is missing from /etc/ufw/after.rules", "degradednetwork public is not protected"])
  await userEvent.setup().click(screen.getByRole("button", { name: "Repair" }))
  expect(await dpPlanned(calls, "Repair the Docker integration")).toEqual({ path: "/api/v1/hosts/h1/actions/firewall.docker.repair/v1/plan", body: { input: {} } })
})

test("foreign DOCKER-USER rules are listed and can be discarded only by asking", async () => {
  const { calls } = dpRender(dpDocker({ enabled: true, health: "broken", foreignRules: ["-A DOCKER-USER -s 192.0.2.0/24 -j DROP"] }))
  const rules = within(await screen.findByRole("list", { name: "Foreign DOCKER-USER rules" })).getAllByRole("listitem")
  expect(rules.map((li) => li.textContent)).toEqual(["-A DOCKER-USER -s 192.0.2.0/24 -j DROP"])
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair and discard foreign rules" }))
  expect(await dpPlanned(calls, "Repair the Docker integration and discard foreign rules")).toEqual({
    path: "/api/v1/hosts/h1/actions/firewall.docker.repair/v1/plan",
    body: { input: { discardForeign: true } },
  })
})

test("without foreign rules the repair cannot discard anything", async () => {
  dpRender(dpDocker({ enabled: true, health: "degraded", issues: [{ level: "degraded", reason: "network public is not protected" }] }))
  await screen.findByRole("button", { name: "Repair" })
  expect(screen.queryByRole("button", { name: "Repair and discard foreign rules" })).toBeNull()
  expect(screen.queryByRole("list", { name: "Foreign DOCKER-USER rules" })).toBeNull()
})

test("manual route rules to containers are listed with what they do not do", async () => {
  dpRender(dpDocker({ manualRoutes: ["allow in on docker0 out on docker0 to 172.17.0.2 port 80 proto tcp"] }))
  const routes = within(await screen.findByRole("list", { name: "Manual route rules" })).getAllByRole("listitem")
  expect(routes.map((li) => li.textContent)).toEqual(["allow in on docker0 out on docker0 to 172.17.0.2 port 80 proto tcp"])
  expect(screen.getByText(/no effect on Docker traffic until a container rule/).textContent).toContain("container rule")
})

test("the networks table says which networks are protected", async () => {
  dpRender(dpDocker())
  const table = await screen.findByRole("table", { name: "Docker networks" })
  const rows = within(table).getAllByRole("row").slice(1).map((r) => within(r).getAllByRole("cell").map((c) => c.textContent))
  expect(rows).toEqual([
    ["bridge", "docker0", "172.17.0.0/16", "Protected"],
    ["public", "dppublic", "203.0.113.0/24", "Not protected: custom bridge name with a non-private subnet"],
  ])
})

test("the private sources editor receives the status", async () => {
  dpRender(dpDocker())
  expect((await screen.findByTestId("docker-sources")).textContent).toBe("h1: 10.0.0.0/8, 100.64.0.0/10")
})

test("a host without Docker shows why and offers nothing", async () => {
  dpRender(dpDocker({ available: false, reason: "the Docker socket is not present", version: undefined, driver: undefined, dockerVariant: undefined, ufwVariant: undefined, rawProtection: false, supported: false, networks: [] }))
  expect((await screen.findByText("Docker is not available on this host: the Docker socket is not present")).textContent).toContain("socket is not present")
  expect(screen.queryByRole("button")).toBeNull()
  expect(screen.queryByTestId("docker-sources")).toBeNull()
  expect(screen.queryByLabelText("Docker detection")).toBeNull()
})

test("a failed read shows the server's reason", async () => {
  dpRender(dpDocker(), {
    [DOCKER]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})

test("the Docker panel has no accessibility violations", async () => {
  const { container } = dpRender(dpDocker({ enabled: true, health: "broken", issues: [{ level: "broken", reason: "the block is missing" }], foreignRules: ["-A DOCKER-USER -j DROP"], manualRoutes: ["allow in on docker0 to 172.17.0.2"] }))
  await screen.findByRole("table", { name: "Docker networks" })
  expect(await axe(container)).toHaveNoViolations()
})
