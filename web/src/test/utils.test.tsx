import { screen } from "@testing-library/react"
import { expect, test } from "vitest"
import { ApiError, request } from "@/api/client"
import { renderWithProviders, stubFetch } from "./utils"

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
