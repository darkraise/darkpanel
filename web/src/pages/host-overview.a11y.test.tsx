import { screen } from "@testing-library/react"
import { test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { HostOverview } from "./host-overview"

test("OP-3: the Overview tab passes axe for an online and a pending host", async () => {
  stubFetch({ ["POST /api/v1/hosts/h1/actions/core.update.status/v1/read"]: { body: { output: { transactions: [] } } } })
  const online = renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  await screen.findByText("No updates yet")
  await expectNoAxeViolations(online.container)
  online.unmount()
  const pending = renderWithProviders(<HostOverview host={makeHost({ id: "h2", state: "pending", online: false, lastSeenAt: null })} />)
  await expectNoAxeViolations(pending.container)
})
