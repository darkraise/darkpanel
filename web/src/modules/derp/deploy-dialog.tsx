import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import { DerpChange } from "./derp-change"

type Mode = "letsencrypt" | "ip"

const MODES: Array<{ value: Mode; label: string }> = [
  { value: "letsencrypt", label: "Let's Encrypt" },
  { value: "ip", label: "IP certificate" },
]

export function DeployDialog({ onClose }: { onClose: () => void }) {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  const [hostId, setHostId] = useState("")
  const [mode, setMode] = useState<Mode>("letsencrypt")
  const [hostName, setHostName] = useState("")
  const [verifyClients, setVerifyClients] = useState(true)

  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  const ready = host !== undefined && hostName.trim() !== ""

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    setInput({ hostId, mode, hostName: hostName.trim(), ...(verifyClients ? {} : { verifyClients: false }) })
  }

  if (input !== null) {
    return <DerpChange action="derp.server.deploy" title={`Deploy a DERP server on ${host?.name ?? hostId}`} input={input} onClose={onClose} />
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Deploy a DERP server</DialogTitle>
          <DialogDescription>Installs a DERP relay on one of your hosts and opens its ports.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit}>
          {hosts.isPending && <Spinner aria-label="Loading the hosts" />}
          {hosts.isError && (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
            </Alert>
          )}
          {hosts.isSuccess && (
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">Host</legend>
              <RadioGroup value={hostId} onValueChange={setHostId} aria-label="Host">
                {hosts.data.hosts.map((h) => (
                  <div key={h.id} className="flex items-center gap-2">
                    <RadioGroupItem id={`derp-deploy-host-${h.id}`} value={h.id} />
                    <Label htmlFor={`derp-deploy-host-${h.id}`}>{h.name}</Label>
                  </div>
                ))}
              </RadioGroup>
            </fieldset>
          )}
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">Certificate mode</legend>
            <RadioGroup value={mode} onValueChange={(value) => setMode(value as Mode)} aria-label="Certificate mode">
              {MODES.map((m) => (
                <div key={m.value} className="flex items-center gap-2">
                  <RadioGroupItem id={`derp-deploy-mode-${m.value}`} value={m.value} />
                  <Label htmlFor={`derp-deploy-mode-${m.value}`}>{m.label}</Label>
                </div>
              ))}
            </RadioGroup>
          </fieldset>
          <div className="flex flex-col gap-2">
            <Label htmlFor="derp-deploy-name">{mode === "letsencrypt" ? "DNS name" : "Public IP address"}</Label>
            <Input id="derp-deploy-name" value={hostName} onChange={(event) => setHostName(event.target.value)} autoComplete="off" />
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="derp-deploy-verify" checked={verifyClients} onCheckedChange={(checked) => setVerifyClients(checked === true)} />
            <Label htmlFor="derp-deploy-verify">Verify clients</Label>
          </div>
          {!verifyClients && (
            <Alert variant="warning" role="status">
              <AlertDescription>Client verification is off: any node that knows the region can relay through it.</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onClose()}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ready}>
              Review
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
