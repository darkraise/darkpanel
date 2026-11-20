import { useQuery } from "@tanstack/react-query"
import { changeError, changeInProgress, getChange, request } from "@/api/client"
import type { Change } from "@/api/types"

export const INSTALL_ACTION = "system.docker.install"
// The install composite's step on the host itself.
const ENGINE_STEP = "system.docker.engine-install"

// How many of the host's recent changes the lookup reads, and how many
// installs among them it reads.
const LOOKUP = 20
const CANDIDATES = 3
// A failed or rolled-back install is shown for a day; after that the panel
// offers a fresh install without dwelling on it.
const RECENT_MS = 24 * 60 * 60 * 1000

// Under "changes", so every change event refreshes it.
export const latestInstallKey = (hostId: string) => ["changes", "docker-install", hostId] as const

// latestInstall is the host's most recent Docker install. The install is a
// controller-side composite with no host of its own; its package step runs
// on the host, so the host's own changes are read and that step names the
// composite as its parent. An install refused before its package step is
// not found here.
export async function latestInstall(hostId: string): Promise<Change | null> {
  const { changes } = await request<{ changes: Change[] }>("GET", `/api/v1/changes?hostId=${encodeURIComponent(hostId)}&limit=${LOOKUP}`)
  const parents = [...new Set(changes.filter((c) => c.action === ENGINE_STEP && c.parentChangeId !== null).map((c) => c.parentChangeId as string))]
  for (const id of parents.slice(0, CANDIDATES)) {
    const parent = await getChange(id)
    if (parent.action === INSTALL_ACTION) return parent
  }
  return null
}

export function useLatestInstall(hostId: string, enabled: boolean) {
  return useQuery({
    queryKey: latestInstallKey(hostId),
    queryFn: async () => relevantInstall(await latestInstall(hostId), Date.now()) ?? null,
    enabled,
    refetchInterval: (q) => (q.state.data != null && changeInProgress(q.state.data) ? 5000 : false),
  })
}

// relevantInstall is an install worth showing on a page load: one still
// running or awaiting its confirmation, or one that failed or rolled back
// within the last day.
export function relevantInstall(c: Change | null | undefined, now: number): Change | undefined {
  if (c == null) return undefined
  if (changeInProgress(c)) return c
  const ended = c.state === "reverted" || changeError(c) !== null
  return ended && now - Date.parse(c.updatedAt) < RECENT_MS ? c : undefined
}
