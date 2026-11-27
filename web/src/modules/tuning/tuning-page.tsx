import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Progress } from "darkraise-ui/components/progress"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { FALLBACK_FOCUS, useFocusFallback } from "@/lib/return-focus"
import { applied } from "@/lib/users"
import { useCanOnHost } from "@/session/can"
import { MIB, normalValue, swapLimit, valueError, type SwapState, type SysctlKey, type SysctlState } from "./tuning"
import { focusableScroller } from "@/lib/table"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
  done: string
  revertible?: boolean
}

function FieldError({ id, children }: { id: string; children: string }) {
  return (
    <p id={id} className="text-sm text-destructive-text">
      {children}
    </p>
  )
}

function Failed({ what, error, onRetry }: { what: string; error: unknown; onRetry: () => void }) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertTitle as="h3">{`Couldn't load ${what}`}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-2">
        {errorMessage(error)}
        <Button size="sm" variant="outline" onClick={onRetry}>
          Try again
        </Button>
      </AlertDescription>
    </Alert>
  )
}

const mib = (bytes: number) => Math.round(bytes / MIB)
const gib = (bytes: number) => (bytes / MIB / 1024).toFixed(1)

function SwapCard({ state, admin, onChange }: { state: SwapState; admin: boolean; onChange: (p: Pending) => void }) {
  const own = state.swaps.find((s) => s.darkpanel)
  const [size, setSize] = useState(String(own === undefined ? 1024 : mib(own.size)))
  const [tried, setTried] = useState(false)
  const limit = swapLimit(state)
  const n = /^\d+$/.test(size) ? Number(size) : NaN
  const error = n >= 256 && n <= limit ? null : limit < 256 ? "Not enough free space on / for a swap file" : `Use 256 to ${limit} MiB`
  const shown = tried || size !== String(own === undefined ? 1024 : mib(own.size)) ? error : null
  const unavailable = state.unavailable ?? ""

  function sized(action: "tuning.swap.create" | "tuning.swap.resize") {
    setTried(true)
    if (error !== null) return
    onChange(
      action === "tuning.swap.create"
        ? { action, title: `Create a ${n} MiB swap file`, input: { sizeMiB: n }, done: "Swap file created" }
        : { action, title: `Resize the swap file to ${n} MiB`, input: { sizeMiB: n }, done: "Swap file resized" },
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h3 className="text-base font-semibold">Swap</h3>
        </CardTitle>
        <CardDescription>{`Memory ${gib(state.memoryTotal)} GiB, ${gib(state.memoryAvailable)} GiB available`}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {state.swaps.length === 0 && <p className="text-sm text-muted-foreground">No swap is on.</p>}
        {state.swaps.map((s) => (
          <div key={s.path} className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="break-all font-mono text-sm">{s.path}</span>
              <span className="text-xs text-muted-foreground">{s.type}</span>
              {s.darkpanel && <Badge variant="outline">Darkpanel</Badge>}
              <span className="text-sm text-muted-foreground">{`${mib(s.used)} of ${mib(s.size)} MiB used`}</span>
            </div>
            <Progress value={s.used} max={Math.max(s.size, 1)} aria-label={`${s.path} used`} getValueLabel={(v, m) => `${Math.round((v / m) * 100)}%`} />
          </div>
        ))}
        {unavailable !== "" ? (
          <Alert role="status">
            <AlertDescription>{unavailable}</AlertDescription>
          </Alert>
        ) : (
          admin && (
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                sized(own === undefined ? "tuning.swap.create" : "tuning.swap.resize")
              }}
              noValidate
            >
              <div className="flex flex-col gap-1">
                <Label htmlFor="swap-size">{own === undefined ? "Swap file size (MiB)" : "/darkpanel.swap size (MiB)"}</Label>
                <Input
                  id="swap-size"
                  inputMode="numeric"
                  value={size}
                  onChange={(e) => setSize(e.target.value)}
                  className="max-w-40"
                  aria-invalid={shown !== null}
                  aria-describedby={shown !== null ? "swap-size-error" : undefined}
                />
              </div>
              <Button type="submit" variant={own === undefined ? "default" : "outline"}>
                {own === undefined ? "Create swap file" : "Resize"}
              </Button>
              {own !== undefined && (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => onChange({ action: "tuning.swap.remove", title: "Remove the swap file", input: {}, done: "Swap file removed" })}
                >
                  Remove
                </Button>
              )}
              {shown !== null && (
                <div className="basis-full">
                  <FieldError id="swap-size-error">{shown}</FieldError>
                </div>
              )}
            </form>
          )
        )}
      </CardContent>
    </Card>
  )
}

function KeyRow({ k, admin, draft, onDraft }: { k: SysctlKey; admin: boolean; draft: string | undefined; onDraft: (v: string) => void }) {
  const id = `sysctl-${k.key}`
  const value = draft ?? k.set ?? ""
  const error = draft === undefined ? null : valueError(k, draft)
  const owner = k.owner ?? ""
  const unavailable = k.unavailable ?? ""
  const later = k.laterFiles ?? []
  return (
    <TableRow>
      <TableCell className="max-w-[24rem] whitespace-normal">
        <span className="break-all font-mono text-xs">{k.key}</span>
        {(k.help ?? "") !== "" && <span className="block text-xs text-muted-foreground">{k.help}</span>}
      </TableCell>
      <TableCell className="break-all font-mono text-xs">{unavailable !== "" ? unavailable : k.live}</TableCell>
      <TableCell>
        {unavailable !== "" ? null : (
          <div className="flex flex-col items-start gap-1">
            {owner !== "" && <Badge variant="outline">{`Managed by ${owner}`}</Badge>}
            {admin && owner === "" && (
              <Input
                id={id}
                aria-label={`${k.key} at boot`}
                value={value}
                placeholder={k.live}
                onChange={(e) => onDraft(e.target.value)}
                aria-invalid={error !== null}
                aria-describedby={error !== null ? `${id}-error` : undefined}
                className="max-w-48 font-mono"
                spellCheck={false}
              />
            )}
            {!admin && owner === "" && <span className="font-mono text-xs">{(k.set ?? "") === "" ? "Not set by Darkpanel" : k.set}</span>}
            {error !== null && <FieldError id={`${id}-error`}>{error}</FieldError>}
            {owner === "" && later.length > 0 && <span className="text-xs text-warning-text">{`${later[later.length - 1] ?? ""} sets it later at boot and wins`}</span>}
          </div>
        )}
      </TableCell>
    </TableRow>
  )
}

// TuningPage is the Tuning tab (UI-8, UX-11): swap with Darkpanel's swap
// file, and kernel parameters whose changed values one revertible change
// sets.
export function TuningPage({ hostId }: { hostId: string }) {
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const swapKey = ["resource", hostId, "tuning.swap.get"]
  const sysctlKey = ["resource", hostId, "tuning.sysctl.get"]
  const swap = useQuery({ queryKey: swapKey, queryFn: () => readAction<SwapState>("tuning.swap.get", 1, {}, hostId) })
  const sysctl = useQuery({ queryKey: sysctlKey, queryFn: () => readAction<SysctlState>("tuning.sysctl.get", 1, {}, hostId) })
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [pending, setPending] = useState<Pending | null>(null)
  useFocusFallback(pending !== null)

  const keys = sysctl.data?.keys ?? []
  const changed = keys.filter((k) => drafts[k.key] !== undefined && normalValue(drafts[k.key] ?? "") !== (k.set ?? ""))
  const invalid = changed.some((k) => valueError(k, drafts[k.key] ?? "") !== null)

  function review() {
    // The input is every key the file holds afterwards.
    const values: Record<string, string> = {}
    for (const k of keys) if ((k.set ?? "") !== "" && (k.owner ?? "") === "") values[k.key] = k.set ?? ""
    for (const k of changed) {
      const v = normalValue(drafts[k.key] ?? "")
      if (v === "") delete values[k.key]
      else values[k.key] = v
    }
    setPending({
      action: "tuning.sysctl.apply",
      title: changed.length === 1 ? `Set ${changed[0]?.key ?? ""}` : `Set ${changed.length} kernel parameters`,
      input: { values },
      done: "Kernel parameters set",
      revertible: true,
    })
  }

  return (
    <div className="flex flex-col gap-6">
      <h2 className="sr-only">Tuning</h2>
      {swap.isPending ? (
        <Skeleton role="status" aria-label="Loading swap" className="h-32 w-full" />
      ) : swap.isError ? (
        <Failed what="swap" error={swap.error} onRetry={() => void swap.refetch()} />
      ) : (
        <SwapCard key={JSON.stringify(swap.data.swaps)} state={swap.data} admin={admin} onChange={setPending} />
      )}
      <section aria-labelledby="tuning-sysctl" className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <h3 id="tuning-sysctl" className="text-base font-semibold">
            Kernel parameters
          </h3>
          {sysctl.data !== undefined && <p className="break-all text-xs text-muted-foreground">{`Darkpanel writes them to ${sysctl.data.file}`}</p>}
        </div>
        {sysctl.isPending ? (
          <div role="status" aria-label="Loading kernel parameters" className="flex flex-col gap-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : sysctl.isError ? (
          <Failed what="kernel parameters" error={sysctl.error} onRetry={() => void sysctl.refetch()} />
        ) : (
          <>
            <Table aria-label="Kernel parameters" ref={focusableScroller("Kernel parameters table")}>
              <TableHeader>
                <TableRow>
                  <TableHead>Key</TableHead>
                  <TableHead>Live</TableHead>
                  <TableHead>Value at boot</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((k) => (
                  <KeyRow key={k.key} k={k} admin={admin} draft={drafts[k.key]} onDraft={(v) => setDrafts((d) => ({ ...d, [k.key]: v }))} />
                ))}
              </TableBody>
            </Table>
            {admin && (
              <div className="flex flex-wrap items-center gap-3">
                <Button {...{ [FALLBACK_FOCUS]: "" }} disabled={changed.length === 0 || invalid} onClick={review}>
                  Review changes
                </Button>
                <span className="text-xs text-muted-foreground">
                  {changed.length === 0 ? "No changes yet" : invalid ? "Fix the values marked above" : `${changed.length} changed`}
                </span>
              </div>
            )}
          </>
        )}
      </section>
      {pending !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => (open ? undefined : setPending(null))}
          title={pending.title}
          target={{ name: pending.action, version: 1, hostId, module: "tuning", revertible: pending.revertible }}
          input={pending.input}
          onApplied={(c: Change) => {
            void client.invalidateQueries({ queryKey: swapKey })
            void client.invalidateQueries({ queryKey: sysctlKey })
            if (pending.action === "tuning.sysctl.apply") setDrafts({})
            if (applied(c)) toast(pending.done)
          }}
        />
      )}
    </div>
  )
}
