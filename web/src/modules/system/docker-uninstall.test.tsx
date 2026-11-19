import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { DockerUninstall } from "./docker-uninstall"

const host = makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })

test("UX-6: integration on offers the firewall step first and warns about published ports", async () => {
  stubFetch({})
  renderWithProviders(<DockerUninstall host={host} docker={{ state: "docker-ce", running: true, containers: 0, images: 0, volumes: 0, labelled: [] }} integrationOn />)
  screen.getByRole("button", { name: "Turn off firewall integration" })
  screen.getByText("Published ports stay reachable until Docker is removed")
  expect(screen.queryByRole("button", { name: "Uninstall" })).toBeNull()
})

test("UX-6: labelled containers are named with links to their pages", () => {
  stubFetch({})
  renderWithProviders(<DockerUninstall host={host} docker={{ state: "docker-ce", running: true, containers: 1, images: 1, volumes: 0, labelled: [{ name: "npm-1", module: "npm" }] }} integrationOn={false} />)
  expect(screen.getByRole("link", { name: "npm-1" }).getAttribute("href")).toBe("/hosts/h1/npm")
})

test("UX-6: purge is off by default, disabled when the daemon is down, and sends purge", async () => {
  const calls = stubFetch({ "POST /api/v1/hosts/h1/actions/system.docker.uninstall/v1/plan": { body: makePlan("system.docker.uninstall") } })
  const { unmount } = renderWithProviders(<DockerUninstall host={host} docker={{ state: "docker-ce", running: false }} integrationOn={false} />)
  expect((screen.getByRole("checkbox", { name: "Delete all containers, images and volumes" }) as HTMLInputElement).disabled).toBe(true)
  unmount()
  renderWithProviders(<DockerUninstall host={host} docker={{ state: "docker-ce", running: true, containers: 3, images: 5, volumes: 2, labelled: [] }} integrationOn={false} />)
  const box = screen.getByRole("checkbox", { name: "Delete all containers, images and volumes" }) as HTMLInputElement
  expect(box.checked).toBe(false)
  await userEvent.click(box)
  await userEvent.click(screen.getByRole("button", { name: "Uninstall" }))
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { purge: true } })
})
