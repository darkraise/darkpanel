import { afterEach, describe, expect, test, vi } from "vitest"
import {
  ApiError,
  applyPlan,
  changeError,
  changeInProgress,
  confirmChange,
  errorMessage,
  getAvailability,
  getChange,
  listEffects,
  listSteps,
  observeEffect,
  readAction,
  readResource,
  releaseChange,
  repairChange,
  request,
  resolveEffect,
  revertChange,
  setCsrfRejectedHandler,
  setCsrfToken,
  setUnauthorizedHandler,
  undoChange,
} from "./client"
import type { Change, Plan } from "./types"

function respond(status: number, body?: unknown, headers: Record<string, string> = {}): Response {
  const h = new Headers(headers)
  if (body !== undefined && !h.has("Content-Type")) {
    h.set("Content-Type", status >= 400 ? "application/problem+json" : "application/json")
  }
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: h })
}

function stub(res: Response | (() => Promise<Response>)) {
  const fetchMock = vi.fn<typeof fetch>()
  if (res instanceof Response) fetchMock.mockResolvedValue(res)
  else fetchMock.mockImplementation(res)
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

function sent(fetchMock: ReturnType<typeof stub>) {
  const call = fetchMock.mock.calls[0]
  if (call === undefined) throw new Error("fetch was not called")
  const [url, init] = call
  return {
    url: String(url),
    method: init?.method,
    credentials: init?.credentials,
    headers: new Headers(init?.headers),
    body: typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
  }
}

const plan: Plan = {
  planId: "p1",
  hostId: null,
  action: "core.token.revoke",
  version: 1,
  inputHash: "h",
  diff: [],
  guards: [],
  expiresAt: "2026-10-19T10:00:00Z",
  confirmation: "tok1",
}

afterEach(() => {
  setCsrfToken(null)
  setUnauthorizedHandler(null)
  setCsrfRejectedHandler(null)
  vi.unstubAllGlobals()
})

describe("request", () => {
  test("sends JSON headers and same-origin credentials, without CSRF on GET", async () => {
    setCsrfToken("csrf-1")
    const fetchMock = stub(respond(200, { status: "ok" }))
    await expect(request("GET", "/healthz")).resolves.toEqual({ status: "ok" })
    const s = sent(fetchMock)
    expect(s.credentials).toBe("same-origin")
    expect(s.headers.get("Content-Type")).toBe("application/json")
    expect(s.headers.get("X-CSRF-Token")).toBeNull()
  })

  test("adds the CSRF token to unsafe methods once it is set", async () => {
    const fetchMock = stub(respond(204))
    await request("POST", "/api/v1/auth/logout")
    expect(sent(fetchMock).headers.get("X-CSRF-Token")).toBeNull()

    setCsrfToken("csrf-1")
    fetchMock.mockResolvedValue(respond(204))
    await expect(request("POST", "/api/v1/auth/logout")).resolves.toBeUndefined()
    const second = fetchMock.mock.calls[1]?.[1]
    expect(new Headers(second?.headers).get("X-CSRF-Token")).toBe("csrf-1")
  })

  test("turns problem details into an ApiError", async () => {
    stub(respond(422, {
      type: "about:blank", title: "Invalid input", status: 422, detail: "invalid request",
      code: "request.invalid_input", errors: [{ path: "/name", message: "required" }],
    }))
    const err = await request("POST", "/x", {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const api = err as ApiError
    expect(api.status).toBe(422)
    expect(api.code).toBe("request.invalid_input")
    expect(api.message).toBe("invalid request")
    expect(api.fields).toEqual([{ path: "/name", message: "required" }])
    expect(api.retryAfterSeconds).toBeNull()
  })

  test("reads Retry-After on a rate-limited answer", async () => {
    stub(respond(429, { type: "about:blank", title: "Too many", status: 429, detail: "slow down", code: "auth.rate_limited" },
      { "Retry-After": "42" }))
    const err = (await request("POST", "/api/v1/auth/login", {}).catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe("auth.rate_limited")
    expect(err.retryAfterSeconds).toBe(42)
  })

  test("calls the unauthorized handler only for auth.required", async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    stub(respond(401, { type: "about:blank", title: "Login failed", status: 401, detail: "bad", code: "auth.invalid_credentials" }))
    await request("POST", "/api/v1/auth/login", {}).catch(() => undefined)
    expect(handler).not.toHaveBeenCalled()

    stub(respond(401, { type: "about:blank", title: "Authentication required", status: 401, detail: "log in", code: "auth.required" }))
    await request("GET", "/api/v1/changes").catch(() => undefined)
    expect(handler).toHaveBeenCalledTimes(1)
  })

  test("ignores auth.required for a request begun before the session changed", async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    setCsrfToken("csrf-old")
    let answer: (res: Response) => void = () => {}
    stub(() => new Promise<Response>((resolve) => {
      answer = resolve
    }))
    const pending = request("GET", "/api/v1/changes").catch((e: unknown) => e)
    setCsrfToken(null)
    setCsrfToken("csrf-new")
    answer(respond(401, { type: "about:blank", title: "Authentication required", status: 401, detail: "log in", code: "auth.required" }))
    expect(((await pending) as ApiError).code).toBe("auth.required")
    expect(handler).not.toHaveBeenCalled()
  })

  test("calls the CSRF-rejected handler only for auth.csrf", async () => {
    const handler = vi.fn()
    setCsrfRejectedHandler(handler)
    stub(respond(403, { type: "about:blank", title: "Forbidden", status: 403, detail: "no", code: "auth.forbidden" }))
    await request("POST", "/api/v1/auth/logout").catch(() => undefined)
    expect(handler).not.toHaveBeenCalled()

    stub(respond(403, { type: "about:blank", title: "CSRF", status: 403, detail: "bad csrf token", code: "auth.csrf" }))
    const err = (await request("POST", "/api/v1/auth/logout").catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe("auth.csrf")
    expect(handler).toHaveBeenCalledTimes(1)
  })

  test("reports an unreachable controller as code network", async () => {
    stub(() => Promise.reject(new TypeError("Failed to fetch")))
    const err = (await request("GET", "/healthz").catch((e: unknown) => e)) as ApiError
    expect(err.status).toBe(0)
    expect(err.code).toBe("network")
  })

  test("names an internal error when a failure has no problem body", async () => {
    stub(new Response("oops", { status: 502, statusText: "Bad Gateway" }))
    const err = (await request("GET", "/healthz").catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe("internal")
    expect(err.message).toBe("Bad Gateway")
  })
})

describe("action helpers", () => {
  test("readAction posts the input to the controller route and returns the output", async () => {
    const fetchMock = stub(respond(200, { output: { entries: [], nextBefore: null } }))
    await expect(readAction("core.audit.list", 1, { limit: 50 })).resolves.toEqual({ entries: [], nextBefore: null })
    const s = sent(fetchMock)
    expect(s.url).toBe("/api/v1/actions/core.audit.list/v1/read")
    expect(s.method).toBe("POST")
    expect(s.body).toEqual({ input: { limit: 50 } })
  })

  test("readAction uses the host route when given a host", async () => {
    const fetchMock = stub(respond(200, { output: null }))
    await readAction("demo.read", 2, {}, "h 1")
    expect(sent(fetchMock).url).toBe("/api/v1/hosts/h%201/actions/demo.read/v2/read")
  })

  test("applyPlan sends the plan id and confirmation to the plan's apply route", async () => {
    const fetchMock = stub(respond(200, { id: "c1", state: "completed" }))
    await applyPlan(plan, "tok1")
    const s = sent(fetchMock)
    expect(s.url).toBe("/api/v1/actions/core.token.revoke/v1/apply")
    expect(s.body).toEqual({ planId: "p1", confirmation: "tok1" })
  })

  test("applyPlan uses the host route and omits a missing confirmation", async () => {
    const fetchMock = stub(respond(200, { id: "c2", state: "pending" }))
    await applyPlan({ ...plan, hostId: "h1", action: "demo.set", confirmation: null })
    const s = sent(fetchMock)
    expect(s.url).toBe("/api/v1/hosts/h1/actions/demo.set/v1/apply")
    expect(s.body).toEqual({ planId: "p1" })
  })

  test("readResource returns the body itself", async () => {
    const fetchMock = stub(respond(200, { hostname: "web-1" }))
    await expect(readResource("h1", "hostinfo", "summary")).resolves.toEqual({ hostname: "web-1" })
    expect(sent(fetchMock).url).toBe("/api/v1/hosts/h1/resources/hostinfo/summary")
  })
})

describe("change helpers", () => {
  test.each([
    ["getChange", () => getChange("c 1"), "GET", "/api/v1/changes/c%201", undefined],
    ["confirmChange", () => confirmChange("c1"), "POST", "/api/v1/changes/c1/confirm", undefined],
    ["revertChange", () => revertChange("c1"), "POST", "/api/v1/changes/c1/revert", undefined],
    ["undoChange", () => undoChange("c1"), "POST", "/api/v1/changes/c1/undo", undefined],
    ["repairChange without a body", () => repairChange("c1"), "POST", "/api/v1/changes/c1/repair", {}],
    ["repairChange with input", () => repairChange("c1", { input: { port: 22 } }), "POST", "/api/v1/changes/c1/repair", { input: { port: 22 } }],
    ["repairChange of a step", () => repairChange("c1", { step: "ports", as: "reverted" }), "POST", "/api/v1/changes/c1/repair",
      { step: "ports", as: "reverted" }],
    ["releaseChange", () => releaseChange("c1", "gone by hand"), "POST", "/api/v1/changes/c1/release", { reason: "gone by hand" }],
    ["observeEffect", () => observeEffect("c1", 2), "GET", "/api/v1/changes/c1/effects/2/observe", undefined],
    ["resolveEffect", () => resolveEffect("c1", 2, { resolution: "accept" }), "POST", "/api/v1/changes/c1/effects/2/resolve",
      { resolution: "accept" }],
    ["getAvailability", () => getAvailability("h 1", "fire wall"), "GET", "/api/v1/hosts/h%201/modules/fire%20wall/availability", undefined],
  ])("%s calls its route", async (_, call, method, url, body) => {
    const fetchMock = stub(respond(200, { ok: true }))
    await expect(call()).resolves.toEqual({ ok: true })
    const s = sent(fetchMock)
    expect([s.method, s.url]).toEqual([method, url])
    expect(s.body).toEqual(body)
    if (method === "POST") expect(s.headers.get("Content-Type")).toBe("application/json")
  })

  test("listEffects and listSteps unwrap their lists", async () => {
    const fetchMock = stub(respond(200, { effects: [{ seq: 1 }] }))
    await expect(listEffects("c1")).resolves.toEqual([{ seq: 1 }])
    expect(sent(fetchMock).url).toBe("/api/v1/changes/c1/effects")
    const stepsMock = stub(respond(200, { steps: [{ id: "ports" }] }))
    await expect(listSteps("c1")).resolves.toEqual([{ id: "ports" }])
    expect(sent(stepsMock).url).toBe("/api/v1/changes/c1/steps")
  })

  test("applyPlan returns a change still applying from a 202 answer", async () => {
    stub(respond(202, { id: "c3", state: "applying" }))
    const change = await applyPlan(plan)
    expect(change.state).toBe("applying")
    expect(changeInProgress(change)).toBe(true)
    expect(changeError(change)).toBeNull()
  })
})

describe("messages", () => {
  test("errorMessage shows an ApiError's detail and a generic line otherwise", () => {
    expect(errorMessage(new ApiError(409, "module.unavailable", "the module is unavailable"))).toBe("the module is unavailable")
    expect(errorMessage(new Error("boom"))).toBe("Something went wrong. Try again.")
  })

  test.each([
    ["completed", null, null],
    ["confirmed", null, null],
    ["pending", null, null],
    ["failed", { code: "exec.failed", message: "the command failed" }, "the command failed"],
    ["interrupted", null, "The change ended in state interrupted."],
    ["applying", null, null],
    ["awaiting_confirmation", null, null],
    ["undoing", null, null],
    ["needs_repair", { code: "core.needs_repair", message: "the change needs repair", cause: "cloud.uncertain_outcome" }, "the change needs repair"],
  ])("changeError for state %s", (state, error, want) => {
    const change = { state, error } as Change
    expect(changeError(change)).toBe(want)
  })
})
