import { useState, type FormEvent } from "react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { DerpChange } from "./derp-change"
import type { DerpServerView, ServerProbe } from "./types"

const CODE_PATTERN = /^[a-z][a-z0-9-]{0,31}$/
const CODE_RULE = "Lower-case letters, digits and hyphens, starting with a letter, at most 32 characters."

function validId(text: string): boolean {
  if (!/^\d+$/.test(text)) return false
  const id = Number(text)
  return id >= 900 && id <= 999
}

export function RegionDialog({ server, probe, onClose }: { server: DerpServerView; probe: ServerProbe; onClose: () => void }) {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  const [idText, setIdText] = useState(server.regionId !== 0 ? String(server.regionId) : "900")
  const [code, setCode] = useState(server.regionCode)
  const [name, setName] = useState("")
  const [omit, setOmit] = useState(false)
  const [acknowledge, setAcknowledge] = useState("")

  const idOk = validId(idText)
  const codeOk = CODE_PATTERN.test(code)
  const trimmedName = name.trim()
  const nameOk = trimmedName.length >= 1 && trimmedName.length <= 64
  const ready = idOk && codeOk && nameOk
  const codeInvalid = !codeOk
  const nameInvalid = name !== "" && !nameOk

  function setOmitDefaults(checked: boolean) {
    setOmit(checked)
    if (!checked) setAcknowledge("")
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    setInput({
      hostId: server.hostId,
      regionId: Number(idText),
      regionCode: code,
      regionName: trimmedName,
      ...(omit ? { omitDefaultRegions: true } : {}),
      ...(omit && acknowledge !== "" ? { acknowledge } : {}),
    })
  }

  if (input !== null) {
    return <DerpChange action="derp.region.set" title={`Publish region ${code}`} input={input} onClose={onClose} />
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Publish a DERP region on ${server.host}`}</DialogTitle>
          <DialogDescription>Adds this server to the tailnet's DERP map as its own region.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit}>
          {probe.warnings.length > 0 && (
            <Alert variant="warning" role="status">
              <AlertDescription>
                <ul className="list-disc pl-5">
                  {probe.warnings.map((warning, i) => (
                    <li key={i}>{warning}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}
          {!probe.ready && (
            <Alert variant="warning" role="status">
              <AlertDescription>The probe has not passed yet; the server refuses to publish the region until it does.</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-col gap-2">
            <Label htmlFor="derp-region-id">Region id</Label>
            <Input
              id="derp-region-id"
              type="number"
              value={idText}
              onChange={(event) => setIdText(event.target.value)}
              aria-invalid={!idOk}
              aria-describedby="derp-region-id-help"
            />
            <span id="derp-region-id-help" className={idOk ? "text-xs text-muted-foreground" : "text-xs text-destructive"}>
              Use a number from 900 to 999.
            </span>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="derp-region-code">Region code</Label>
            <Input
              id="derp-region-code"
              value={code}
              onChange={(event) => setCode(event.target.value)}
              aria-invalid={codeInvalid}
              aria-describedby="derp-region-code-help"
              autoComplete="off"
            />
            <span id="derp-region-code-help" className={codeInvalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
              {CODE_RULE}
            </span>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="derp-region-name">Region name</Label>
            <Input
              id="derp-region-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              aria-invalid={nameInvalid}
              aria-describedby="derp-region-name-help"
              autoComplete="off"
            />
            <span id="derp-region-name-help" className={nameInvalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
              Use 1 to 64 characters.
            </span>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="derp-region-omit" checked={omit} onCheckedChange={(checked) => setOmitDefaults(checked === true)} />
            <Label htmlFor="derp-region-omit">Omit default regions</Label>
          </div>
          {omit && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="derp-region-ack">Acknowledgement</Label>
              <Input
                id="derp-region-ack"
                value={acknowledge}
                onChange={(event) => setAcknowledge(event.target.value)}
                aria-describedby="derp-region-ack-help"
                autoComplete="off"
              />
              <span id="derp-region-ack-help" className="text-xs text-muted-foreground">
                {`To use only this region, type: nodes will relay only through ${code === "" ? "<region code>" : code}.`}
              </span>
            </div>
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
