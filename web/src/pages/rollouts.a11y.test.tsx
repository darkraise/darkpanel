import { screen } from "@testing-library/react"
import { test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { RolloutsPage } from "./rollouts"

test("OP-3: the rollouts page passes axe with a failed rollout and when empty", async () => {
  const failed = { id: "r1", version: "1.2.3", channel: "beta", state: "failed", error: { code: "x", message: "web-2 failed" }, createdAt: "2026-11-11T08:00:00Z", updatedAt: "2026-11-11T08:01:00Z",
    hosts: [{ hostId: "h1", position: 0, state: "failed", planId: null, operationId: null, error: { code: "x", message: "web-2 failed" } }] }
  stubFetch({ ["POST /api/v1/actions/core.update.rollouts/v1/read"]: { body: { output: { rollouts: [failed] } } }, ["POST /api/v1/actions/core.host.list/v1/read"]: { body: { output: { hosts: [] } } } })
  const one = renderWithProviders(<RolloutsPage />)
  await screen.findByRole("region", { name: "1.2.3 (beta)" })
  await expectNoAxeViolations(one.container)
  one.unmount()
  stubFetch({ ["POST /api/v1/actions/core.update.rollouts/v1/read"]: { body: { output: { rollouts: [] } } }, ["POST /api/v1/actions/core.host.list/v1/read"]: { body: { output: { hosts: [] } } } })
  const empty = renderWithProviders(<RolloutsPage />)
  await screen.findByText("No rollouts yet")
  await expectNoAxeViolations(empty.container)
})
