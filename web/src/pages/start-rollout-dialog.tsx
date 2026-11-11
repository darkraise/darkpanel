import { useEffect, useId, useRef, useState, type FormEvent, type Ref } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { HOSTS_QUERY_KEY, ROLLOUTS_QUERY_KEY, changeError, changeInProgress, errorMessage, listHosts } from "@/api/client"
import { ChangeDialog } from "@/components/change-dialog"
import { useReturnFocus } from "@/lib/return-focus"
import { ReleaseFields, releaseError, type Release } from "./update-now-dialog"

type Phase = "form" | "plan"

const EMPTY: Release = { version: "", channel: "stable" }

// StartRolloutTrigger is the "Start rollout" button alone; whoever renders it
// owns the flow it opens.
export function StartRolloutTrigger({ onOpen, ref }: { onOpen: (button: HTMLButtonElement) => void; ref?: Ref<HTMLButtonElement> }) {
  const id = useId()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  return (
    <>
      <Button ref={ref} disabled={hosts.data === undefined} aria-describedby={hosts.isError ? id : undefined} onClick={(e) => onOpen(e.currentTarget)}>
        Start rollout
      </Button>
      {hosts.isError && (
        <p id={id} className="self-center text-sm text-destructive">
          {`The host list could not be read: ${errorMessage(hosts.error)}`}
        </p>
      )}
    </>
  )
}

// StartRolloutFlow is mounted for one opening, from the form to the change
// dialog's result, and calls onClose when its last dialog closes.
export function StartRolloutFlow({ onClose }: { onClose: () => void }) {
  const client = useQueryClient()
  const id = useId()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [phase, setPhase] = useState<Phase>("form")
  const [chosen, setChosen] = useState<Set<string>>(new Set())
  const [release, setRelease] = useState<Release>(EMPTY)
  const [errors, setErrors] = useState<{ hosts?: string; version?: string }>({})
  const [firstHost, setFirstHost] = useState<HTMLButtonElement | null>(null)
  const focused = useRef(false)

  // darkraise-ui's focus trap focuses the first tabbable on mount and the
  // release fields autofocus the version, so the first host is focused a frame
  // later, once the dialog's portal has mounted it, and only once: a host
  // list refresh must not pull focus from the field being typed in.
  useEffect(() => {
    if (focused.current || firstHost === null) return
    const frame = requestAnimationFrame(() => {
      focused.current = true
      firstHost.focus()
    })
    return () => cancelAnimationFrame(frame)
  }, [firstHost])

  const enrolled = (hosts.data?.hosts ?? []).filter((h) => h.state === "enrolled")
  const selected = enrolled.filter((h) => chosen.has(h.id)).map((h) => h.id)

  function toggle(hostId: string, on: boolean) {
    setChosen((prev) => {
      const next = new Set(prev)
      if (on) next.add(hostId)
      else next.delete(hostId)
      return next
    })
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const next = { hosts: selected.length === 0 ? "Choose at least one host." : undefined, version: releaseError(release.version) }
    setErrors(next)
    if (next.hosts !== undefined) {
      if (firstHost !== null) firstHost.focus()
      else requestAnimationFrame(() => document.getElementById(`${id}-hosts-error`)?.focus())
      return
    }
    if (next.version !== undefined) {
      document.getElementById("release-version")?.focus()
      return
    }
    setPhase("plan")
  }

  return (
    <>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : onClose())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Start a rollout</DialogTitle>
            <DialogDescription>Hosts update one at a time.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
            <fieldset className="flex flex-col gap-2" aria-describedby={errors.hosts !== undefined ? `${id}-hosts-error` : undefined}>
              <legend className="text-sm font-medium">Hosts</legend>
              <div className="flex max-h-60 flex-col gap-2 overflow-y-auto">
                {enrolled.map((h, i) => (
                  <div key={h.id} className="flex items-center gap-2">
                    <Checkbox
                      id={`${id}-host-${h.id}`}
                      ref={i === 0 ? setFirstHost : undefined}
                      checked={chosen.has(h.id)}
                      onCheckedChange={(c) => toggle(h.id, c === true)}
                      aria-invalid={errors.hosts !== undefined}
                    />
                    <Label htmlFor={`${id}-host-${h.id}`} className="break-all">
                      {h.isLocal ? `${h.name} (updates first)` : h.name}
                    </Label>
                  </div>
                ))}
              </div>
              {errors.hosts !== undefined && (
                <p id={`${id}-hosts-error`} tabIndex={-1} className="text-sm text-destructive">
                  {enrolled.length === 0 ? "No host is enrolled yet, so no rollout can start." : errors.hosts}
                </p>
              )}
            </fieldset>
            <ReleaseFields value={release} onChange={setRelease} errors={errors} />
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => (next ? undefined : onClose())}
        title="Start a rollout"
        target={{ name: "core.update.rollout", version: 1 }}
        input={{ hosts: selected, version: release.version, channel: release.channel }}
        onApplied={(change) => {
          if (changeError(change) === null && !changeInProgress(change)) void client.invalidateQueries({ queryKey: ROLLOUTS_QUERY_KEY })
        }}
      />
    </>
  )
}

export function StartRolloutButton() {
  const [open, setOpen] = useState(false)
  const opener = useReturnFocus(open)
  return (
    <>
      <StartRolloutTrigger ref={opener} onOpen={() => setOpen(true)} />
      {open && <StartRolloutFlow onClose={() => setOpen(false)} />}
    </>
  )
}
