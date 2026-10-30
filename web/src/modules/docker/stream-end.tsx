import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import type { StreamEnd } from "./stream"

function endMessage(end: StreamEnd, done: string): string {
  switch (end.reason) {
    case "done":
      return done
    case "overflow":
      return "The stream fell behind and was closed."
    case "expired":
      return "The stream reached its time limit."
    case "revoked":
      return "Access to the stream was revoked."
    case "error":
      return end.error !== undefined && end.error.detail !== "" ? end.error.detail : "The stream failed."
  }
}

export function StreamEndNotice({ end, done, onReconnect }: { end: StreamEnd; done: string; onReconnect: () => void }) {
  return (
    <Alert variant={end.reason === "done" ? "default" : "warning"} role="status">
      <AlertDescription className="flex items-center justify-between gap-2">
        <span>{endMessage(end, done)}</span>
        <Button size="sm" variant="outline" onClick={onReconnect}>
          Reconnect
        </Button>
      </AlertDescription>
    </Alert>
  )
}
