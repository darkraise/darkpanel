import { useState } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { TailscaleChange } from "./tailscale-change"

interface Pending {
  action: string
  title: string
}

// PackagePanel installs the Tailscale package on a host where tailscaled does
// not answer and updates it where it does, through the change dialog.
export function PackagePanel({ hostId, available }: { hostId: string; available: boolean }) {
  const [pending, setPending] = useState<Pending | null>(null)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Package</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {available ? (
          <div>
            <Button variant="outline" onClick={() => setPending({ action: "tailscale.package.update", title: "Update Tailscale" })}>
              Update Tailscale
            </Button>
          </div>
        ) : (
          <>
            <p>Tailscale is not installed on this host, or tailscaled does not answer.</p>
            <div>
              <Button onClick={() => setPending({ action: "tailscale.package.install", title: "Install Tailscale" })}>Install Tailscale</Button>
            </div>
          </>
        )}
      </CardContent>
      {pending !== null && (
        <TailscaleChange hostId={hostId} action={pending.action} title={pending.title} input={{}} onClose={() => setPending(null)} />
      )}
    </Card>
  )
}
