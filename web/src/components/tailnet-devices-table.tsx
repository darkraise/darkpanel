import { useEffect, useRef, type RefObject } from "react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage } from "@/api/client"
import type { TailnetDevice } from "@/api/types"
import { formatTime } from "@/lib/format"
import { deviceName, firstLabel } from "@/lib/tailnet-devices"
import { STACKED_BODY, STACKED_CELL, STACKED_FRAME, STACKED_HEAD, STACKED_ROW, STACKED_TABLE } from "@/lib/table"

export interface TailnetDevicesTableProps {
  devices: TailnetDevice[] | undefined
  isLoading: boolean
  error: unknown
  onForget: (device: TailnetDevice) => void
  emptyText: string
  // focusFallback takes focus when a forget leaves the list empty.
  focusFallback: RefObject<HTMLElement | null>
}

// After a forget the change dialog closes and keeps focus in its leftover
// nodes until its exit animation ends, then drops it on the body, so the
// table keeps placing focus until it stays put.
const FOCUS_TICK_MS = 50
const FOCUS_TRIES = 100
const FOCUS_STEADY = 3

export function TailnetDevicesTable({ devices, isLoading, error, onForget, emptyText, focusFallback }: TailnetDevicesTableProps) {
  const pending = useRef<{ stableId: string; index: number } | null>(null)
  const buttons = useRef(new Map<string, HTMLButtonElement>())
  // A new forget must stop an earlier one still placing focus, or that one
  // pulls focus out of the new change dialog.
  const placing = useRef<number | null>(null)
  const stopPlacing = () => {
    if (placing.current !== null) window.clearInterval(placing.current)
    placing.current = null
  }

  useEffect(() => {
    const p = pending.current
    if (p === null || devices === undefined || devices.some((d) => d.stableId === p.stableId)) return
    pending.current = null
    const target = () => {
      const next = devices[Math.min(p.index, devices.length - 1)]
      return next === undefined ? focusFallback.current : (buttons.current.get(next.stableId) ?? null)
    }
    let tries = 0
    let steady = 0
    const id = window.setInterval(() => {
      const el = target()
      if (el === null || tries++ >= FOCUS_TRIES) {
        stopPlacing()
        return
      }
      if (document.activeElement === el) {
        if (++steady >= FOCUS_STEADY) stopPlacing()
        return
      }
      steady = 0
      el.focus()
    }, FOCUS_TICK_MS)
    placing.current = id
    return stopPlacing
  }, [devices, focusFallback])

  if (isLoading) return <Spinner aria-label="Loading the devices" />
  if (error !== null && error !== undefined) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(error)}</AlertDescription>
      </Alert>
    )
  }
  if (devices === undefined || devices.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Device list</h2>
        <EmptyState title="No remembered devices" description={emptyText} />
      </>
    )
  }
  return (
    <div className={STACKED_FRAME}>
      <Table className={STACKED_TABLE}>
        <TableHeader className={STACKED_HEAD}>
          <TableRow>
            <TableHead>Device</TableHead>
            <TableHead>Remembered</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className={STACKED_BODY}>
          {devices.map((d, i) => (
            <TableRow key={d.stableId} className={STACKED_ROW}>
              <TableCell data-label="Device" className={`${STACKED_CELL} min-w-48 whitespace-normal wrap-anywhere`}>
                <div className="flex flex-col">
                  <span className="font-medium">{d.nodeName === "" ? "Unnamed device" : firstLabel(d.nodeName)}</span>
                  {d.nodeName !== "" && <span className="text-sm text-muted-foreground">{d.nodeName}</span>}
                  <span className="font-mono text-sm text-muted-foreground">{d.stableId}</span>
                </div>
              </TableCell>
              <TableCell data-label="Remembered" className={STACKED_CELL}>
                {formatTime(d.rememberedAt)}
              </TableCell>
              <TableCell data-label="Last used" className={STACKED_CELL}>
                {formatTime(d.lastUsedAt)}
              </TableCell>
              <TableCell className={STACKED_CELL}>
                <Button
                  ref={(el) => {
                    if (el === null) buttons.current.delete(d.stableId)
                    else buttons.current.set(d.stableId, el)
                  }}
                  variant="outline"
                  size="sm"
                  aria-label={`Forget device ${deviceName(d)}`}
                  onClick={() => {
                    stopPlacing()
                    pending.current = { stableId: d.stableId, index: i }
                    onForget(d)
                  }}
                >
                  Forget
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
