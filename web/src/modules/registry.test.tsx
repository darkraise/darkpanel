import { Fragment, Suspense, type ComponentType } from "react"
import { screen, waitFor } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { moduleRegistry } from "./registry"
import type { ContainerRef } from "./types"

const regContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

// regComponents lists every lazy component of the registry, named.
function regComponents(): Array<[string, ComponentType]> {
  const out: Array<[string, ComponentType]> = []
  for (const entry of moduleRegistry) {
    const { routes: Routes, global, hostCreate } = entry
    if (Routes !== undefined) out.push([`${entry.id} routes`, () => <Routes hostId="h1" />])
    if (global !== undefined) out.push([`${entry.id} global page`, () => <global.routes />])
    if (hostCreate !== undefined) out.push([`${entry.id} host-create`, () => <hostCreate.render value={{}} onChange={() => {}} />])
    for (const action of entry.containerActions ?? []) {
      out.push([`${action.id} container action`, () => <action.render container={regContainer} onDone={() => {}} />])
    }
  }
  return out
}

describe("module registry", () => {
  // One Suspense boundary for all of them: each boundary waits out React's
  // fallback throttle once.
  test("every lazy component of the registry loads", async () => {
    stubFetch({})
    renderWithProviders(
      <Suspense fallback={<p>Loading the components</p>}>
        {regComponents().map(([name, Component]) => (
          <Fragment key={name}>
            <Component />
          </Fragment>
        ))}
      </Suspense>,
    )
    await waitFor(() => expect(screen.queryByText("Loading the components")).toBeNull())
  })

  test("registers the extension points the modules' pages need", () => {
    const globals = moduleRegistry.flatMap((e) => (e.global === undefined ? [] : [e.global.path]))
    expect(globals).toEqual(["tailnet", "derp", "cloudflare"])
    const actions = moduleRegistry.flatMap((e) => (e.containerActions ?? []).map((a) => a.id))
    expect(actions).toEqual(["firewall.allow", "firewall.deny", "npm.expose", "cloudflare.expose"])
    expect(moduleRegistry.flatMap((e) => (e.hostCreate === undefined ? [] : [e.hostCreate.id]))).toEqual(["tailnet.join"])
    expect(moduleRegistry.filter((e) => e.routes === undefined && e.global === undefined)).toEqual([])
  })
})
