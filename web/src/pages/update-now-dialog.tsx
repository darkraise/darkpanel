import { useState, type FormEvent } from "react"
import { Link } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { ROLLOUTS_QUERY_KEY, changeError, changeInProgress } from "@/api/client"
import type { Channel, Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { useReturnFocus } from "@/lib/return-focus"

const SEMVER = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/

// Exported beside the dialog because Contracts fix its home; the start-rollout
// form shares it.
// eslint-disable-next-line react-refresh/only-export-components
export function releaseError(version: string): string | undefined {
  return SEMVER.test(version) ? undefined : "Enter a version such as 1.2.3."
}

export interface Release {
  version: string
  channel: Channel
}

export function ReleaseFields({
  value,
  onChange,
  errors,
}: {
  value: Release
  onChange: (v: Release) => void
  errors: { version?: string }
}) {
  return (
    <>
      <div className="flex flex-col gap-2">
        <Label htmlFor="release-version">Version</Label>
        <Input
          id="release-version"
          placeholder="1.2.3"
          autoComplete="off"
          autoFocus
          value={value.version}
          onChange={(e) => onChange({ ...value, version: e.target.value })}
          aria-invalid={errors.version !== undefined}
          aria-describedby={errors.version !== undefined ? "release-version-error" : undefined}
        />
        {errors.version !== undefined && (
          <p id="release-version-error" className="text-sm text-destructive">
            {errors.version}
          </p>
        )}
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">Channel</legend>
        <RadioGroup value={value.channel} onValueChange={(v) => onChange({ ...value, channel: v as Channel })} aria-label="Channel" className="flex flex-wrap gap-4">
          <div className="flex items-center gap-2">
            <RadioGroupItem id="release-channel-stable" value="stable" />
            <Label htmlFor="release-channel-stable">Stable</Label>
          </div>
          <div className="flex items-center gap-2">
            <RadioGroupItem id="release-channel-beta" value="beta" />
            <Label htmlFor="release-channel-beta">Beta</Label>
          </div>
        </RadioGroup>
      </fieldset>
    </>
  )
}

type Phase = "closed" | "form" | "plan"

const EMPTY: Release = { version: "", channel: "stable" }

export function UpdateNowButton({ host }: { host: Host }) {
  const client = useQueryClient()
  const [phase, setPhase] = useState<Phase>("closed")
  const [release, setRelease] = useState<Release>(EMPTY)
  const [errors, setErrors] = useState<{ version?: string }>({})
  const [started, setStarted] = useState(false)
  const opener = useReturnFocus(phase !== "closed")

  if (host.state !== "enrolled") return null

  function reset() {
    setPhase("closed")
    setRelease(EMPTY)
    setErrors({})
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const version = releaseError(release.version)
    setErrors({ version })
    if (version !== undefined) {
      document.getElementById("release-version")?.focus()
      return
    }
    setPhase("plan")
  }

  return (
    <>
      <Button ref={opener} onClick={() => setPhase("form")}>
        Update now
      </Button>
      {started && (
        <Link to="/rollouts" className="self-center text-sm font-medium underline underline-offset-4">
          Follow the rollout
        </Link>
      )}
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{`Update ${host.name}`}</DialogTitle>
            <DialogDescription>The update runs as a rollout of this one host.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
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
        title={`Update ${host.name}`}
        target={{ name: "core.update.rollout", version: 1 }}
        input={{ hosts: [host.id], version: release.version, channel: release.channel }}
        onApplied={(change) => {
          if (changeError(change) !== null || changeInProgress(change)) return
          setStarted(true)
          void client.invalidateQueries({ queryKey: ROLLOUTS_QUERY_KEY })
        }}
      />
    </>
  )
}
