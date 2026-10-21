// The controller API's JSON shapes (spec section 12).
export type Scope = "read" | "operate" | "admin"
export interface User { id: string; username: string; role: string; state: string; createdAt: string }
export interface TokenInfo { id: string; name: string; scope: string; createdAt: string; expiresAt: string; lastUsedAt: string | null; revokedAt: string | null }
export interface NewToken extends TokenInfo { token: string }
export interface TokenList { tokens: TokenInfo[] }
export interface Session { user: User; csrfToken: string | null; expiresAt: string | null; token: TokenInfo | null }
export interface ModuleState { id: string; version: number; enabled: boolean; available: boolean; reason: string | null; checkedAt: string | null }
export interface Host { id: string; name: string; isLocal: boolean; online: boolean; modules: ModuleState[]; ingestion: { ok: boolean; since: string; error: string | null } | null }
export interface HostList { hosts: Host[] }
export interface DiffLine { op: string; text: string }
export interface Guard { id: string; ok: boolean; message: string }
export interface Plan { planId: string; hostId: string | null; action: string; version: number; inputHash: string; diff: DiffLine[]; guards: Guard[]; expiresAt: string; confirmation: string | null }
export interface ChangeError { code: string; message: string }
export interface Change<O = unknown> { id: string; hostId: string | null; action: string; version: number; class: string; state: string; actor: string; createdAt: string; updatedAt: string; revertDeadline: string | null; output: O | null; error: ChangeError | null; compensatedBy: string | null }
export interface AuditEntry { id: string; at: string; actor: string; hostId: string | null; action: string; version: number; phase: string; changeId: string | null; input: unknown; result: string; errorCode: string | null; source: string | null }
export interface AuditList { entries: AuditEntry[]; nextBefore: string | null }
export interface ListenerState { tailscaleBound: boolean; message: string }
export interface FieldError { path: string; message: string }
export interface Problem { type: string; title: string; status: number; detail: string; code: string; errors?: FieldError[] }
