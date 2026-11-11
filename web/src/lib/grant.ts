import type { EnrollmentGrant } from "@/api/types"

export function isGrant(output: unknown): output is EnrollmentGrant {
  if (typeof output !== "object" || output === null) return false
  const o = output as Record<string, unknown>
  return typeof o.token === "string" && typeof o.expiresAt === "string" && typeof o.host === "object" && o.host !== null
}
