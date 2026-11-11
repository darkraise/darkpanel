import { expect, test } from "vitest"
import type { Host, Rollout, UpdatePolicy, UpdateTransaction } from "./types"

test("the Host type carries the R27 fields the controller sends", () => {
  const host: Host = {
    id: "h1", name: "web-1", isLocal: false, state: "pending", online: false, lastSeenAt: null,
    agentVersion: null, protocolVersion: null, os: null, arch: null, supported: true, modules: [], ingestion: null,
  }
  const policy: UpdatePolicy = { hostId: "h1", channel: "beta", schedule: { days: ["mon"], time: "03:00" } }
  const tx: UpdateTransaction = {
    id: "t1", operationId: "t1", parentId: null, kind: "update", fromVersion: "1.0.0", toVersion: "1.1.0", targetVersion: "1.1.0",
    state: "committed", error: null, startedAt: "2026-11-11T07:00:00Z", updatedAt: "2026-11-11T07:05:00Z",
  }
  const rollout: Rollout = { id: "r1", version: "1.1.0", channel: "stable", state: "running", error: null, createdAt: "2026-11-11T07:00:00Z", updatedAt: "2026-11-11T07:00:00Z", hosts: [] }
  expect([host.state, policy.channel, tx.kind, rollout.state]).toEqual(["pending", "beta", "update", "running"])
})
