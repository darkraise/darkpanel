import { ApiError, errorMessage, readAction } from "@/api/client"
import { isStatusOutput, PANEL_ACTION_VERSION, type StatusOutput } from "./types"

// readExposureStatus reads panel.exposure.status on the controller. The
// banner and the panel page share it under panelStatusKey.
export function readExposureStatus(): Promise<StatusOutput> {
  return readAction<unknown>("panel.exposure.status", PANEL_ACTION_VERSION, {}).then((body) => {
    if (!isStatusOutput(body)) throw new Error("The controller answered with an unexpected panel exposure status.")
    return body
  })
}

// messageOf is the text an alert shows for a failed read or request.
export function messageOf(error: unknown): string {
  if (error instanceof ApiError) return errorMessage(error)
  return error instanceof Error ? error.message : String(error)
}
