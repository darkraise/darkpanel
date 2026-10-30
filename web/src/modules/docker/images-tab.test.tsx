import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler, type StubResponse } from "@/test/utils"
import { dkImage } from "./fixtures"
import { ImagesTab } from "./images-tab"
import type { DockerImage } from "./types"

const IM_LIST = "GET /api/v1/hosts/h1/resources/docker/images"
const IM_ACTIONS = ["docker.image.pull", "docker.image.remove", "docker.image.prune"]

function imPlan(call: FetchCall): StubResponse {
  const action = call.path.split("/")[6] ?? ""
  const confirmation = action === "docker.image.pull" ? null : "confirm"
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

function imRender(images: DockerImage[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [IM_LIST]: { body: { images } },
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    ...Object.fromEntries(IM_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/plan`, imPlan])),
    ...routes,
  })
  const result = renderWithProviders(<ImagesTab hostId="h1" />)
  return { calls, ...result }
}

function imPlanCall(calls: FetchCall[]): { path: string; body: unknown } {
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

const imNginx = dkImage({ id: "sha256:0123456789abcdef0123456789abcdef", tags: ["nginx:1.27", "nginx:latest"], size: 104857600, created: "2026-10-01T08:00:00Z", containers: ["web", "proxy"], dangling: false })
const imRedis = dkImage({ id: "sha256:fedcba9876543210fedcba9876543210", tags: ["redis:7.4"], size: 52428800, created: "2026-09-20T10:30:00Z", containers: [], dangling: false })
const imOld = dkImage({ id: "sha256:aaaabbbbccccddddeeeeffff00001111", tags: [], size: 1610612736, created: "2026-08-01T00:00:00Z", containers: [], dangling: true })

test("the table shows each image's tags, short id, size, date and users", async () => {
  imRender([imNginx, imOld])
  const rows = within(await screen.findByRole("table", { name: "Docker images" })).getAllByRole("row")
  expect(rows).toHaveLength(3)
  const cells = (row: HTMLElement | undefined) => within(row as HTMLElement).getAllByRole("cell").slice(0, 5).map((c) => c.textContent)
  expect(cells(rows[1])).toEqual(["nginx:1.27, nginx:latest", "0123456789ab", "100 MiB", "2026-10-01", "web, proxy"])
  expect(cells(rows[2])).toEqual(["<none>dangling", "aaaabbbbcccc", "1.5 GiB", "2026-08-01", "—"])
})

test("Remove plans the first tag, or the id of an untagged image, and is off while a container uses the image", async () => {
  const { calls } = imRender([imNginx, imRedis, imOld])
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Docker images" })
  expect(screen.getByRole("button", { name: "Remove image nginx:1.27" }).hasAttribute("disabled")).toBe(true)

  await user.click(screen.getByRole("button", { name: "Remove image redis:7.4" }))
  await within(await screen.findByRole("dialog", { name: "Remove image redis:7.4" })).findByLabelText("Planned changes")
  expect(imPlanCall(calls)).toEqual({ path: "/api/v1/hosts/h1/actions/docker.image.remove/v1/plan", body: { input: { image: "redis:7.4" } } })
})

test("Remove of an untagged image plans its full id", async () => {
  const { calls } = imRender([imOld])
  await userEvent.setup().click(await screen.findByRole("button", { name: `Remove image ${imOld.id}` }))
  await within(await screen.findByRole("dialog", { name: `Remove image ${imOld.id}` })).findByLabelText("Planned changes")
  expect(imPlanCall(calls).body).toEqual({ input: { image: imOld.id } })
})

test("Pull image asks for a reference and plans the pull", async () => {
  const { calls } = imRender([imRedis])
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Pull image" }))
  const form = await screen.findByRole("form", { name: "Pull an image" })
  expect(within(form).getByRole("button", { name: "Plan pull" }).hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Image"), " redis:7.5 ")
  await user.click(within(form).getByRole("button", { name: "Plan pull" }))
  await within(await screen.findByRole("dialog", { name: "Pull redis:7.5" })).findByLabelText("Planned changes")
  expect(imPlanCall(calls)).toEqual({ path: "/api/v1/hosts/h1/actions/docker.image.pull/v1/plan", body: { input: { image: "redis:7.5" } } })
})

test("Prune images plans the prune when an unused dangling image exists and is off otherwise", async () => {
  const first = imRender([imNginx, imOld])
  await userEvent.setup().click(await screen.findByRole("button", { name: "Prune images" }))
  await within(await screen.findByRole("dialog", { name: "Prune dangling images" })).findByLabelText("Planned changes")
  expect(imPlanCall(first.calls)).toEqual({ path: "/api/v1/hosts/h1/actions/docker.image.prune/v1/plan", body: { input: {} } })
  first.unmount()

  imRender([imNginx, imRedis])
  expect((await screen.findByRole("button", { name: "Prune images" })).hasAttribute("disabled")).toBe(true)
})

test("a host without images says so and still offers a pull", async () => {
  imRender([])
  expect((await screen.findByText("No images")).textContent).toBe("No images")
  expect(screen.getByRole("button", { name: "Pull image" })).toBeTruthy()
})

test("a failed read shows the server's reason", async () => {
  imRender([], {
    [IM_LIST]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the docker module is unavailable on this host", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the docker module is unavailable on this host")
})

test("the images table has no accessibility violations", async () => {
  const { container } = imRender([imNginx, imRedis, imOld])
  await screen.findByRole("table", { name: "Docker images" })
  expect(await axe(container)).toHaveNoViolations()
})
