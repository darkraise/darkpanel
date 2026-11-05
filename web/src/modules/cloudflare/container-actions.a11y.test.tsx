import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import { cfConnector, cfTunnel } from "./fixtures"

const CA_HOST = "/api/v1/hosts/h1"
const CA_TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const CA_CONNECTORS = `POST ${CA_HOST}/actions/cloudflare.connector.list/v1/read`
const CA_INSPECT = `POST ${CA_HOST}/actions/docker.container.inspect/v1/read`
const CA_NETWORKS = `GET ${CA_HOST}/resources/docker/networks`
const CA_DOWN = { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Cloudflare account is not connected", code: "cloud.not_connected" } }

const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function caRender(routes: Record<string, StubHandler> = {}) {
  stubFetch({
    [CA_TUNNELS]: { body: { output: { tunnels: [cfTunnel({ id: "t1", name: "darkpanel-a" })] } } },
    [CA_CONNECTORS]: { body: { output: { connectors: [cfConnector({ tunnel: "t1" })] } } },
    [CA_INSPECT]: {
      body: { output: dkDetail({ id: "c1", ports: [{ privatePort: 80, proto: "tcp" }], networks: ["bridge", "backend"].map((network) => ({ network, networkId: "id-" + network, container: "web" })) }) },
    },
    [CA_NETWORKS]: { body: { networks: [dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }), dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" })] } },
    ...routes,
  })
  return renderWithProviders(<ExposeAction container={caContainer} onDone={() => {}} />)
}

const caViews: { state: string; routes: Record<string, StubHandler>; ready: () => Promise<unknown> }[] = [
  { state: "the Expose via tunnel form", routes: {}, ready: () => screen.findByRole("form", { name: "Expose container via tunnel" }) },
  { state: "a host with no tunnel", routes: { [CA_TUNNELS]: { body: { output: { tunnels: [] } } } }, ready: () => screen.findByText("No Cloudflare tunnel exists yet. Create a tunnel first.") },
  { state: "a host with no connector", routes: { [CA_CONNECTORS]: { body: { output: { connectors: [] } } } }, ready: () => screen.findByText("No Cloudflare connector runs on this host. Deploy one first.") },
  { state: "a failed read", routes: { [CA_TUNNELS]: CA_DOWN }, ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ routes, ready }) => {
  const { container } = caRender(routes)
  await ready()
  await expectNoAxeViolations(container)
})

const caGaps: { state: string; routes: Record<string, StubHandler>; link: string }[] = [
  { state: "no tunnel", routes: { [CA_TUNNELS]: { body: { output: { tunnels: [] } } } }, link: "Open the tunnels page" },
  { state: "no connector", routes: { [CA_CONNECTORS]: { body: { output: { connectors: [] } } } }, link: "Open the connectors page" },
]

test.each(caGaps)("with $state, Tab reaches the link to the page that fixes it", async ({ routes, link }) => {
  caRender(routes)
  await screen.findByRole("link", { name: link })
  expect((await tabSequence(userEvent.setup(), 1))[0]).toBe(link)
})
