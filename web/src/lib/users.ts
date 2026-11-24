import { changeError, changeInProgress } from "@/api/client"
import type { Change, DirectoryUser, Grant, GrantRole, HostGroup, Invitation, SignInMethod } from "@/api/types"

export function applied(change: Change): boolean {
  return changeError(change) === null && !changeInProgress(change)
}

function isInvitation(v: unknown): v is Invitation {
  if (typeof v !== "object" || v === null) return false
  const o = v as Record<string, unknown>
  return typeof o.token === "string" && o.token !== "" && typeof o.expiresAt === "string" && typeof o.user === "object" && o.user !== null
}

export function invitationOf(change: Change): Invitation | null {
  return isInvitation(change.output) ? change.output : null
}

// An access draft: the role on every host, if any, and the role per group.
export interface AccessDraft {
  all: GrantRole | null
  groups: Record<string, GrantRole>
}

export function draftOf(grants: Grant[]): AccessDraft {
  const draft: AccessDraft = { all: null, groups: {} }
  for (const g of grants) {
    if (g.target === "*") draft.all = g.role
    else draft.groups[g.target] = g.role
  }
  return draft
}

export function grantsOf(draft: AccessDraft): Grant[] {
  const grants: Grant[] = draft.all === null ? [] : [{ role: draft.all, target: "*" }]
  for (const [target, role] of Object.entries(draft.groups)) grants.push({ role, target })
  return grants
}

export function enrollLink(token: string): string {
  return `${window.location.origin}/enroll#${token}`
}

const GRANT_ROLE = { viewer: "Viewer", operator: "Operator" } as const

// accessText is a user's access in a line: "Operator on web, db; Viewer on
// all hosts". An admin's grants do not apply.
export function accessText(user: Pick<DirectoryUser, "role" | "grants">, groups: HostGroup[] | undefined): string {
  if (user.role === "admin") return "All hosts"
  if (user.grants.length === 0) return "No access"
  const names = new Map((groups ?? []).map((g) => [g.id, g.name]))
  const parts: string[] = []
  for (const role of ["operator", "viewer"] as const) {
    const targets = user.grants.filter((g) => g.role === role).map((g) => (g.target === "*" ? "all hosts" : (names.get(g.target) ?? g.target)))
    if (targets.length > 0) parts.push(`${GRANT_ROLE[role]} on ${targets.join(", ")}`)
  }
  return parts.join("; ")
}

const METHOD: Record<SignInMethod, string> = { password: "Password and code", oidc: "OIDC", tailscale: "Tailscale" }

export function methodsText(methods: SignInMethod[]): string {
  return methods.length === 0 ? "None yet" : methods.map((m) => METHOD[m] ?? m).join(", ")
}
