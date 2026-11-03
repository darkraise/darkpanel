import { ApiError, errorMessage, readAction } from "@/api/client"
import { isInstanceStatus, NPM_ACTION_VERSION, type InstanceStatus } from "./types"

// readInstanceStatus reads npm.instance.status on a host. The page and the
// Instance panel share it under npmKey(hostId, "instance.status").
export function readInstanceStatus(hostId: string): Promise<InstanceStatus> {
  return readAction<unknown>("npm.instance.status", NPM_ACTION_VERSION, {}, hostId).then((body) => {
    if (!isInstanceStatus(body)) throw new Error("The host answered with an unexpected NPM instance status.")
    return body
  })
}

// messageOf is the text an alert shows for a failed read or request.
export function messageOf(error: unknown): string {
  if (error instanceof ApiError) return errorMessage(error)
  return error instanceof Error ? error.message : String(error)
}
