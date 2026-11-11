import { screen } from "@testing-library/react"
import { expect, test } from "vitest"
import { ApiError, request } from "@/api/client"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "./utils"

test("stubFetch answers by method and path and records each call", async () => {
  const calls = stubFetch({
    "POST /api/v1/x": { body: { ok: true } },
    "GET /api/v1/y": (call) => ({ status: 200, body: { path: call.path } }),
  })
  await expect(request("POST", "/api/v1/x", { a: 1 })).resolves.toEqual({ ok: true })
  await expect(request("GET", "/api/v1/y?limit=2")).resolves.toEqual({ path: "/api/v1/y?limit=2" })
  expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(["POST /api/v1/x", "GET /api/v1/y?limit=2"])
  expect(calls[0]?.body).toEqual({ a: 1 })
  expect(calls[0]?.headers.get("Content-Type")).toBe("application/json")
})

test("stubFetch answers an unknown route with a not_found problem", async () => {
  stubFetch({})
  const err = (await request("GET", "/api/v1/missing").catch((e: unknown) => e)) as ApiError
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(404)
  expect(err.code).toBe("not_found")
})

test("stubFetch holds an asynchronous handler's response until it settles", async () => {
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  stubFetch({
    "GET /api/v1/slow": async () => {
      await gate
      return { body: { done: true } }
    },
  })
  let settled = false
  const pending = request("GET", "/api/v1/slow").then((v) => {
    settled = true
    return v
  })
  await new Promise((resolve) => setTimeout(resolve, 0))
  expect(settled).toBe(false)
  release()
  await expect(pending).resolves.toEqual({ done: true })
})

test("stubFetch sends no body for 204", async () => {
  stubFetch({ "POST /api/v1/auth/logout": { status: 204 } })
  await expect(request("POST", "/api/v1/auth/logout")).resolves.toBeUndefined()
})

test("renderWithProviders renders at the path with a location probe", () => {
  renderWithProviders(<p>hello</p>, { path: "/audit?x=1" })
  expect(screen.getByText("hello").textContent).toBe("hello")
  expect(screen.getByTestId("location").textContent).toBe("/audit?x=1")
})

test("makeHost fills every Host field and applies overrides", () => {
  const h = makeHost({ id: "h9", state: "pending", lastSeenAt: null })
  expect(h).toEqual({
    id: "h9", name: "web-1", isLocal: false, state: "pending", online: true, lastSeenAt: null,
    agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null,
  })
})

test("makePlan and makeChange build a plan and a completed change for an action", () => {
  expect(makePlan("core.host.revoke", { confirmation: "web-1" })).toMatchObject({ planId: "p1", action: "core.host.revoke", confirmation: "web-1", guards: [] })
  expect(makeChange("core.host.revoke", { id: "h1" })).toMatchObject({ id: "c1", action: "core.host.revoke", state: "completed", output: { id: "h1" } })
})
