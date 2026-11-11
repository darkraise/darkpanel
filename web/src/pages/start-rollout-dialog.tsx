import { useEffect, useId, useRef, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { HOSTS_QUERY_KEY, ROLLOUTS_QUERY_KEY, changeError, changeInProgress, listHosts } from "@/api/client"
import { ChangeDialog } from "@/components/change-dialog"
import { ReleaseFields, releaseError, type Release } from "./update-now-dialog"

type Phase = "closed" | "form" | "plan"

const EMPTY: Release = { version: "", channel: "stable" }

export function StartRolloutButton() {
  const client = useQueryClient()
  const id = useId()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [phase, setPhase] = useState<Phase>("closed")
  const [chosen, setChosen] = useState<Set<string>>(new Set())
  const [release, setRelease] = useState<Release>(EMPTY)
  const [errors, setErrors] = useState<{ hosts?: string; version?: string }>({})
  const firstHost = useRef<HTMLButtonElement>(null)

  // darkraise-ui's focus trap focuses the first tabbable on mount and the
  // release fields autofocus the version, so the first host is focused a frame later.
  useEffect(() => {
    if (phase !== "form") return
    const frame = requestAnimationFrame(() => firstHost.current?.focus())
    return () => cancelAnimationFrame(frame)
  }, [phase])

  const enrolled = (hosts.data?.hosts ?? []).filter((h) => h.state === "enrolled")
  const selected = enrolled.filter((h) => chosen.has(h.id)).map((h) => h.id)

  function reset() {
    setPhase("closed")
    setChosen(new Set())
    setRelease(EMPTY)
    setErrors({})
  }

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
      firstHost.current?.focus()
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
      <Button disabled={hosts.data === undefined} onClick={() => setPhase("form")}>
        Start rollout
      </Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : reset())}>
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
                      ref={i === 0 ? firstHost : undefined}
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
                <p id={`${id}-hosts-error`} className="text-sm text-destructive">
                  {errors.hosts}
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
        onOpenChange={(next) => (next ? undefined : reset())}
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
