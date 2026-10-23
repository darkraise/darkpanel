import type {
  Change,
  Effect,
  FieldError,
  HostList,
  ListenerState,
  ModuleAvailability,
  ObserveResult,
  Plan,
  Problem,
  ReleaseResult,
  RepairBody,
  RepairResult,
  Resolution,
  Step,
  TokenList,
} from "./types"

export const CSRF_HEADER = "X-CSRF-Token"

export const HOSTS_QUERY_KEY = ["hosts"] as const
export const TOKENS_QUERY_KEY = ["tokens"] as const

// The query keys of one change; connectEvents invalidates them on the
// change and composite.step events.
export const changeQueryKey = (id: string) => ["change", id] as const
export const stepsQueryKey = (id: string) => ["steps", id] as const
export const effectsQueryKey = (id: string) => ["effects", id] as const

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"])
const SUCCESSFUL_STATES = new Set(["completed", "confirmed", "pending"])
// A change ends in these states by a repair, an undo stopped at an accepted
// effect or a release: outcomes an operator chose, although a repaired or
// released change keeps the error it needed repair for.
const RESOLVED_STATES = new Set(["repaired", "kept", "abandoned"])
// A change in one of these states has not ended: an asynchronous apply
// answers with the change applying, and an undo with it undoing.
const IN_PROGRESS_STATES = new Set(["applying", "awaiting_confirmation", "undoing", "recovering", "releasing"])

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
let generation = 0
let unauthorizedHandler: (() => void) | null = null
let csrfRejectedHandler: (() => void) | null = null

// Every login and logout sets the CSRF token and so starts a new session
// generation. An auth.required answer to a request begun in an earlier
// generation must not end the current session.
export function setCsrfToken(token: string | null): void {
  csrfToken = token
  generation++
}

export function sessionGeneration(): number {
  return generation
}

export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

export function setCsrfRejectedHandler(handler: (() => void) | null): void {
  csrfRejectedHandler = handler
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const startedIn = generation
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
    if (err.code === "auth.required" && startedIn === generation) unauthorizedHandler?.()
    if (err.code === "auth.csrf") csrfRejectedHandler?.()
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

// applyPlan applies a plan. Composites, long-running and task actions answer
// 202 with the change still applying; the change events say when it ends.
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

function changePath(id: string, tail = ""): string {
  return `/api/v1/changes/${encodeURIComponent(id)}${tail}`
}

export function getChange(id: string): Promise<Change> {
  return request<Change>("GET", changePath(id))
}

export function confirmChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/confirm"))
}

export function revertChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/revert"))
}

export function undoChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/undo"))
}

// repairChange answers the change a repair started, or a plan that the
// caller applies with applyPlan.
export function repairChange(id: string, body: RepairBody = {}): Promise<RepairResult> {
  return request<RepairResult>("POST", changePath(id, "/repair"), body)
}

export function releaseChange(id: string, reason: string): Promise<ReleaseResult> {
  return request<ReleaseResult>("POST", changePath(id, "/release"), { reason })
}

export async function listEffects(id: string): Promise<Effect[]> {
  const res = await request<{ effects: Effect[] }>("GET", changePath(id, "/effects"))
  return res.effects
}

// observeEffect is the read-only preview of a resolution.
export function observeEffect(id: string, seq: number): Promise<ObserveResult> {
  return request<ObserveResult>("GET", changePath(id, `/effects/${seq}/observe`))
}

export function resolveEffect(id: string, seq: number, body: { resolution: Resolution; resource?: string }): Promise<Effect> {
  return request<Effect>("POST", changePath(id, `/effects/${seq}/resolve`), body)
}

export async function listSteps(id: string): Promise<Step[]> {
  const res = await request<{ steps: Step[] }>("GET", changePath(id, "/steps"))
  return res.steps
}

export function getAvailability(hostId: string, module: string): Promise<ModuleAvailability> {
  return request<ModuleAvailability>("GET", `/api/v1/hosts/${encodeURIComponent(hostId)}/modules/${encodeURIComponent(module)}/availability`)
}

export function errorMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong. Try again."
}

// changeInProgress reports whether a change has not ended yet.
export function changeInProgress(change: Change): boolean {
  return IN_PROGRESS_STATES.has(change.state)
}

// changeError is the failure a change ended with, or null while it
// succeeded, was resolved or is still in progress. A reverted change failed
// only when it carries an error, as after a failed verify; one an operator
// or the confirmation timer reverted carries none.
export function changeError(change: Change): string | null {
  if (SUCCESSFUL_STATES.has(change.state) || IN_PROGRESS_STATES.has(change.state) || RESOLVED_STATES.has(change.state)) return null
  if (change.state === "reverted") return change.error?.message ?? null
  return change.error?.message ?? `The change ended in state ${change.state}.`
}
