import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react"
import { RotateCw, X } from "lucide-react"
import { Button } from "darkraise-ui/components/button"
import { request } from "@/api/client"
import { useStepUp } from "@/components/step-up-dialog"
import { StatusPill } from "@/components/visual/status-pill"
import {
  AGENT_TOO_OLD,
  chunks,
  CLOSED_BY_YOU,
  closedFor,
  COULD_NOT_OPEN,
  FOCUS_SHORTCUT,
  HOST_OFFLINE,
  KEYS,
  NOT_OPENED,
  terminalUrl,
  withCtrl,
  type Closed,
  type OpenSurface,
  type TerminalSurface,
} from "./terminal"

type Phase = { kind: "asking" } | { kind: "connecting" } | { kind: "connected" } | { kind: "closed"; closed: Closed }

const openXterm: OpenSurface = async (el) => (await import("./xterm-surface")).openXterm(el)

export const STEP_UP_REASON = "Opening a terminal needs your authenticator code"

// useNarrow says whether the viewport is below 768 px, where the key bar shows.
function useNarrow(): boolean {
  const query = "(max-width: 767px)"
  const [narrow, setNarrow] = useState(() => window.matchMedia(query).matches)
  useEffect(() => {
    const m = window.matchMedia(query)
    const on = () => setNarrow(m.matches)
    m.addEventListener?.("change", on)
    return () => m.removeEventListener?.("change", on)
  }, [])
  return narrow
}

export interface TerminalViewProps {
  hostId: string
  hostName: string
  online: boolean
  // The agent reports the console module; an older agent cannot open a terminal.
  reported: boolean
  openSurface?: OpenSurface
}

// TerminalView is a root shell on the host through the controller's bridge
// (UI-5, UX-8): a fresh code opens each terminal, every key goes to the
// shell but Ctrl+Shift+L, which moves focus to the toolbar.
export function TerminalView({ hostId, hostName, online, reported, openSurface = openXterm }: TerminalViewProps) {
  const { stepUp } = useStepUp()
  const area = useRef<HTMLDivElement>(null)
  const toolbar = useRef<HTMLDivElement>(null)
  const surface = useRef<TerminalSurface | null>(null)
  const socket = useRef<WebSocket | null>(null)
  const ctrl = useRef(false)
  const closeSocket = useRef<() => void>(() => {})
  const mounted = useRef(false)
  const started = useRef(-1)
  const [ctrlOn, setCtrlOn] = useState(false)
  const [phase, setPhase] = useState<Phase>({ kind: "asking" })
  const [attempt, setAttempt] = useState(0)
  const narrow = useNarrow()

  const send = useCallback((data: string) => {
    const ws = socket.current
    if (ws === null || ws.readyState !== WebSocket.OPEN) return
    let text = data
    if (ctrl.current) {
      ctrl.current = false
      setCtrlOn(false)
      text = withCtrl(data)
    }
    for (const part of chunks(new TextEncoder().encode(text))) ws.send(part)
  }, [])

  // The surface lives as long as the view; each attempt opens a new socket.
  useEffect(() => {
    let live = true
    const unsubscribe: (() => void)[] = []
    const el = area.current
    if (el === null) return
    void Promise.resolve(openSurface(el)).then((s) => {
      if (!live) {
        s.dispose()
        return
      }
      surface.current = s
      unsubscribe.push(
        s.onData(send),
        s.onResize((cols, rows) => {
          const ws = socket.current
          if (ws !== null && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }))
        }),
      )
    })
    let observer: ResizeObserver | undefined
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(() => surface.current?.fit())
      observer.observe(el)
    }
    return () => {
      live = false
      observer?.disconnect()
      for (const u of unsubscribe) u()
      surface.current?.dispose()
      surface.current = null
    }
  }, [openSurface, send])

  // An attempt starts once, however often its effect runs (React runs it
  // twice in development); it lives while the view is mounted.
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      socket.current?.close(1000, "page left")
      socket.current = null
    }
  }, [])

  useEffect(() => {
    if (started.current === attempt) return
    started.current = attempt
    const live = () => mounted.current && started.current === attempt
    let opened = false
    let byUser = false
    let exit: number | null = null
    async function open() {
      if (!online) return setPhase({ kind: "closed", closed: HOST_OFFLINE })
      if (!reported) return setPhase({ kind: "closed", closed: AGENT_TOO_OLD })
      setPhase({ kind: "asking" })
      let ticket = ""
      const ok = await stepUp(STEP_UP_REASON, async (code) => {
        ticket = (await request<{ ticket: string }>("POST", `/api/v1/hosts/${encodeURIComponent(hostId)}/channels/console/terminal/ticket`, { code })).ticket
      })
      if (!live()) return
      if (!ok || ticket === "") return setPhase({ kind: "closed", closed: NOT_OPENED })
      setPhase({ kind: "connecting" })
      const s = surface.current
      const ws = new WebSocket(terminalUrl(hostId, ticket, s?.cols ?? 80, s?.rows ?? 24))
      ws.binaryType = "arraybuffer"
      socket.current = ws
      ws.onopen = () => {
        if (!live()) return
        opened = true
        setPhase({ kind: "connected" })
        surface.current?.focus()
      }
      ws.onmessage = (ev: MessageEvent<ArrayBuffer | string>) => {
        if (typeof ev.data === "string") {
          try {
            const msg = JSON.parse(ev.data) as { type?: string; code?: number }
            if (msg.type === "exit" && typeof msg.code === "number") exit = msg.code
          } catch {
            // Only exit messages come as text.
          }
          return
        }
        surface.current?.write(new Uint8Array(ev.data))
      }
      ws.onclose = (ev: CloseEvent) => {
        if (socket.current === ws) socket.current = null
        if (!live()) return
        const closed = byUser ? CLOSED_BY_YOU : !opened ? COULD_NOT_OPEN : closedFor(ev.reason, exit)
        setPhase({ kind: "closed", closed })
        surface.current?.write(`\r\n\x1b[2m[${closed.text}]\x1b[0m\r\n`)
      }
    }
    void open()
    const close = () => {
      byUser = true
      socket.current?.close(1000, "closed by the user")
    }
    closeSocket.current = close
  }, [attempt, hostId, online, reported, stepUp])

  // reconnect drops the open terminal, if any, and opens a new one.
  function reconnect() {
    const ws = socket.current
    socket.current = null
    ws?.close(1000, "reconnecting")
    setAttempt((n) => n + 1)
  }

  function onKeyDownCapture(e: KeyboardEvent<HTMLDivElement>) {
    if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "l") {
      e.preventDefault()
      e.stopPropagation()
      toolbar.current?.focus()
    }
  }

  const state =
    phase.kind === "asking" ? (
      <StatusPill status="info" label="Waiting for your code" />
    ) : phase.kind === "connecting" ? (
      <StatusPill status="info" label="Connecting…" />
    ) : phase.kind === "connected" ? (
      <StatusPill status="ok" label="Connected" />
    ) : (
      <StatusPill status="off" label={phase.closed.label} />
    )

  return (
    <section aria-label={`Terminal on ${hostName}`} className="flex flex-col gap-2">
      <div ref={toolbar} role="toolbar" aria-label="Terminal" tabIndex={-1} className="flex flex-wrap items-center gap-2 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="font-mono text-sm">{hostName}</span>
        <span role="status">{state}</span>
        {(phase.kind !== "closed" || phase.closed.reconnect) && (
          <Button size="sm" variant="outline" onClick={reconnect}>
            <RotateCw aria-hidden="true" />
            Reconnect
          </Button>
        )}
        {(phase.kind === "connected" || phase.kind === "connecting") && (
          <Button size="sm" variant="outline" onClick={() => closeSocket.current()}>
            <X aria-hidden="true" />
            Close
          </Button>
        )}
        <span className="ms-auto text-xs text-muted-foreground">{`${FOCUS_SHORTCUT} moves focus here`}</span>
      </div>
      {phase.kind === "closed" && (
        <p role="alert" className="text-sm">
          {phase.closed.text}
        </p>
      )}
      <div
        ref={area}
        data-testid="terminal-area"
        onKeyDownCapture={onKeyDownCapture}
        className={`${narrow ? "h-[calc(100dvh-25rem)]" : "h-[calc(100dvh-20rem)]"} min-h-64 overflow-hidden rounded-md border border-border`}
      />
      {narrow && (
        <div role="group" aria-label="Terminal keys" className="flex flex-wrap gap-1">
          <Button size="sm" variant="outline" onClick={() => send("\x1b")}>
            Esc
          </Button>
          <Button size="sm" variant="outline" onClick={() => send("\t")}>
            Tab
          </Button>
          <Button
            size="sm"
            variant={ctrlOn ? "default" : "outline"}
            aria-pressed={ctrlOn}
            onClick={() => {
              ctrl.current = !ctrl.current
              setCtrlOn(ctrl.current)
              surface.current?.focus()
            }}
          >
            Ctrl
          </Button>
          {KEYS.slice(2).map(([label, name, seq]) => (
            <Button key={name} size="sm" variant="outline" aria-label={name} onClick={() => send(seq)}>
              {label}
            </Button>
          ))}
        </div>
      )}
    </section>
  )
}
