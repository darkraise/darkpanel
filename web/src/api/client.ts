import type { Change, FieldError, HostList, ListenerState, Plan, Problem, TokenList } from "./types"

export const CSRF_HEADER = "X-CSRF-Token"

export const HOSTS_QUERY_KEY = ["hosts"] as const
export const TOKENS_QUERY_KEY = ["tokens"] as const

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"])
const SUCCESSFUL_STATES = new Set(["completed", "confirmed", "pending"])

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: FieldError[]
  readonly retryAfterSeconds: number | null

  constructor(status: number, code: string, message: string, fields: FieldError[] = [], retryAfterSeconds: number | null = null) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.fields = fields
    this.retryAfterSeconds = retryAfterSeconds
  }
}

let csrfToken: string | null = null
let unauthorizedHandler: (() => void) | null = null

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", "Content-Type": "application/json" }
  if (UNSAFE_METHODS.has(method) && csrfToken !== null) headers[CSRF_HEADER] = csrfToken
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, "network", "The controller cannot be reached. Check that it is running.")
  }
  if (!res.ok) {
    const err = await problemFrom(res)
    if (err.code === "auth.required") unauthorizedHandler?.()
    throw err
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

async function problemFrom(res: Response): Promise<ApiError> {
  let problem: Partial<Problem> = {}
  if ((res.headers.get("Content-Type") ?? "").includes("json")) {
    try {
      problem = (await res.json()) as Partial<Problem>
    } catch {
      problem = {}
    }
  }
  const retryAfter = Number.parseInt(res.headers.get("Retry-After") ?? "", 10)
  return new ApiError(
    res.status,
    problem.code ?? "internal",
    problem.detail || res.statusText || "The request failed.",
    problem.errors ?? [],
    Number.isNaN(retryAfter) ? null : retryAfter,
  )
}

function actionPath(name: string, version: number, op: "plan" | "apply" | "read", hostId?: string): string {
  const tail = `/actions/${encodeURIComponent(name)}/v${version}/${op}`
  return hostId === undefined ? `/api/v1${tail}` : `/api/v1/hosts/${encodeURIComponent(hostId)}${tail}`
}

export async function readAction<O>(name: string, version: number, input: object = {}, hostId?: string): Promise<O> {
  const res = await request<{ output: O }>("POST", actionPath(name, version, "read", hostId), { input })
  return res.output
}

export function planAction(name: string, version: number, input: object, hostId?: string): Promise<Plan> {
  return request<Plan>("POST", actionPath(name, version, "plan", hostId), { input })
}

export function applyPlan<O>(plan: Plan, confirmation?: string): Promise<Change<O>> {
  const body: { planId: string; confirmation?: string } = { planId: plan.planId }
  if (confirmation !== undefined) body.confirmation = confirmation
  return request<Change<O>>("POST", actionPath(plan.action, plan.version, "apply", plan.hostId ?? undefined), body)
}

export function readResource<O>(hostId: string, module: string, resource: string): Promise<O> {
  const path = [hostId, "resources", module, resource].map(encodeURIComponent).join("/")
  return request<O>("GET", `/api/v1/hosts/${path}`)
}

export function listHosts(): Promise<HostList> {
  return readAction<HostList>("core.host.list", 1)
}

export function listTokens(): Promise<TokenList> {
  return readAction<TokenList>("core.token.list", 1)
}

export function getListenerState(): Promise<ListenerState> {
  return request<ListenerState>("GET", "/api/v1/controller/listeners")
}

export function errorMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong. Try again."
}

export function changeError(change: Change): string | null {
  if (SUCCESSFUL_STATES.has(change.state)) return null
  return change.error?.message ?? `The change ended in state ${change.state}.`
}
