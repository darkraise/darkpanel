import { useQuery } from "@tanstack/react-query"
import { changeError, changeInProgress, listSteps, request } from "@/api/client"
import type { Change } from "@/api/types"

export const INSTALL_ACTION = "system.docker.install"

// How many recent controller-side changes the lookup reads, and how many
// installs among them it reads the steps of.
const LOOKUP = 20
const CANDIDATES = 3
// A failed or rolled-back install is shown for a day; after that the panel
// offers a fresh install without dwelling on it.
const RECENT_MS = 24 * 60 * 60 * 1000

// Under "changes", so every change event refreshes it.
export const latestInstallKey = (hostId: string) => ["changes", "docker-install", hostId] as const

// latestInstall is the host's most recent Docker install. The install is a
// controller-side composite with no host of its own, so the recent
// controller changes are read and each install's steps name its host.
export async function latestInstall(hostId: string): Promise<Change | null> {
  const { changes } = await request<{ changes: Change[] }>("GET", `/api/v1/changes?limit=${LOOKUP}`)
  for (const c of changes.filter((c) => c.action === INSTALL_ACTION).slice(0, CANDIDATES)) {
    const steps = await listSteps(c.id)
    if (steps.some((s) => s.host === hostId)) return c
  }
  return null
}

// useLatestInstall reads the host's latest install that relevantInstall
// keeps, or null.
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
