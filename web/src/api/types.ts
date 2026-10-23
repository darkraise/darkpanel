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
export interface Guard { id: string; ok: boolean; message: string; overridable: boolean; overridden: boolean }
export interface Plan { planId: string; hostId: string | null; action: string; version: number; inputHash: string; diff: DiffLine[]; guards: Guard[]; expiresAt: string; confirmation: string | null }
export interface ChangeError { code: string; message: string; cause?: string }
export interface Change<O = unknown> { id: string; hostId: string | null; action: string; version: number; class: string; state: string; actor: string; createdAt: string; updatedAt: string; revertDeadline: string | null; output: O | null; error: ChangeError | null; compensatedBy: string | null; parentChangeId: string | null; owner: string | null; repairedBy: string | null; repairOutcome: string | null; override: unknown }
// The lifecycle shapes of spec 1.3's wire contract.
export interface StateDoc { state: unknown; version?: unknown }
export type EffectKind = "create" | "modify" | "delete"
export type EffectState = "intended" | "made" | "skipped" | "unresolved" | "undoing" | "undone" | "accepted" | "abandoned"
export type Resolution = "accept" | "reconcile"
export interface Effect { changeId: string; seq: number; kind: EffectKind; resource: string; local: boolean; inverseLocal: boolean; commitBy: string | null; state: EffectState; reason: string; before: StateDoc; after: StateDoc | null; post: StateDoc | null; undoTo: StateDoc | null; undoResource: string; current: StateDoc | null; resolution: string; resolvedBy: string; resolvedAt: string | null }
export interface Candidate { resource: string; state: StateDoc }
export interface Observation { current: StateDoc; resource: string; matches: number; candidates: Candidate[] | null; exclusive: boolean }
export interface ObserveResult { effect: Effect; observation: Observation; allowed: Resolution[] | null }
// A step's action is core.ActionRef, which the controller encodes as one "<name>/v<version>" string.
export interface Step { id: string; ordinal: number; host: string; action: string; state: string; attempt: number; childChangeId: string; earlierChildChangeIds: string[] | null; childDeadline: string | null; error: ChangeError | null }
export type RepairBody = { input?: Record<string, unknown> } | { step: string; as: "reverted" | "applied" }
export interface RepairResult { change?: Change; plan?: Plan }
export interface ReleaseResult { change: Change; abandoned: Effect[] }
export interface ActionAvailability { name: string; available: boolean; reason: string | null }
export interface ModuleAvailability { module: string; available: boolean; reason: string | null; actions: ActionAvailability[] }
export interface AuditEntry { id: string; at: string; actor: string; hostId: string | null; action: string; version: number; phase: string; changeId: string | null; input: unknown; result: string; errorCode: string | null; source: string | null }
export interface AuditList { entries: AuditEntry[]; nextBefore: string | null }
export interface ListenerState { tailscaleBound: boolean; message: string }
export interface FieldError { path: string; message: string }
export interface Problem { type: string; title: string; status: number; detail: string; code: string; errors?: FieldError[] }
