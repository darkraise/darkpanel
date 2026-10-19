import type { ReactElement } from "react"
import { render, type RenderResult } from "@testing-library/react"
import { QueryClient } from "@tanstack/react-query"
import { MemoryRouter, useLocation } from "react-router"
import { vi } from "vitest"
import { Providers } from "@/providers"

export interface FetchCall {
  method: string
  path: string
  headers: Headers
  body: unknown
}

export interface StubResponse {
  status?: number
  body?: unknown
  headers?: Record<string, string>
}

export type StubHandler = StubResponse | ((call: FetchCall) => StubResponse | Promise<StubResponse>)

function toResponse({ status = 200, body, headers = {} }: StubResponse): Response {
  const h = new Headers(headers)
  if (body !== undefined && !h.has("Content-Type")) {
    h.set("Content-Type", status >= 400 ? "application/problem+json" : "application/json")
  }
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: h })
}

export function stubFetch(routes: Record<string, StubHandler>): FetchCall[] {
  const calls: FetchCall[] = []
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const href = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
      const url = new URL(href, "http://localhost")
      const method = (init?.method ?? "GET").toUpperCase()
      const call: FetchCall = {
        method,
        path: url.pathname + url.search,
        headers: new Headers(init?.headers),
        body: typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
      }
      calls.push(call)
      const handler = routes[`${method} ${url.pathname}`]
      if (handler === undefined) {
        return toResponse({
          status: 404,
          body: { type: "about:blank", title: "Not found", status: 404, detail: `no stub for ${method} ${url.pathname}`, code: "not_found" },
        })
      }
      return toResponse(typeof handler === "function" ? await handler(call) : handler)
    }),
  )
  return calls
}

export function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.pathname + location.search}</output>
}

export function renderWithProviders(ui: ReactElement, { path = "/" }: { path?: string } = {}): RenderResult & { client: QueryClient } {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const result = render(
    <MemoryRouter initialEntries={[path]}>
      <Providers client={client}>
        {ui}
        <LocationProbe />
      </Providers>
    </MemoryRouter>,
  )
  return { ...result, client }
}
