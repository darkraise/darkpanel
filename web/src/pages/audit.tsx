import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { useInfiniteQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Timeline, TimelineConnector, TimelineContent, TimelineIndicator, TimelineItem, TimelineTime, TimelineTitle } from "darkraise-ui/components/timeline"
import { errorMessage, readAction } from "@/api/client"
import type { AuditEntry, AuditList } from "@/api/types"
import { Fact } from "@/components/visual/fact"
import { StatusDot } from "@/components/visual/status-dot"
import type { Status } from "@/components/visual/status"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"

const PAGE_SIZE = 50

const RESULT: Record<string, { status: Status; label: string }> = {
  ok: { status: "ok", label: "Succeeded" },
  error: { status: "error", label: "Failed" },
  override: { status: "warn", label: "Overridden" },
}

function Entry({ entry: e }: { entry: AuditEntry }) {
  const result = RESULT[e.result] ?? { status: "off", label: e.result }
  const action = `${e.action}/v${e.version}`
  return (
    <TimelineItem>
      {/* The kit hides the indicator as decoration; here it carries the entry's result. */}
      <TimelineIndicator aria-hidden={false}>
        <StatusDot status={result.status} label={result.label} />
      </TimelineIndicator>
      <TimelineConnector />
      <TimelineContent>
        <TimelineTitle className="flex-wrap gap-x-2">
          {e.changeId === null ? (
            <span className="font-mono break-all">{action}</span>
          ) : (
            <Link to={`/changes/${encodeURIComponent(e.changeId)}`} className="font-mono break-all underline-offset-4 hover:underline">
              {action}
            </Link>
          )}
          <span className="text-muted-foreground font-normal">{e.phase}</span>
        </TimelineTitle>
        <div className="flex flex-wrap gap-1">
          <Fact label="Actor" value={e.actor} mono />
          {e.hostId !== null && <Fact label="Host" value={e.hostId} mono />}
          {e.errorCode !== null && <Fact label="Error" value={e.errorCode} mono />}
        </div>
        <TimelineTime dateTime={e.at}>{formatTime(e.at)}</TimelineTime>
      </TimelineContent>
    </TimelineItem>
  )
}

function AuditBody() {
  const audit = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }) =>
      readAction<AuditList>("core.audit.list", 1, pageParam === null ? { limit: PAGE_SIZE } : { limit: PAGE_SIZE, before: pageParam }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextBefore,
  })
  const moreRef = useRef<HTMLButtonElement>(null)
  const endRef = useRef<HTMLParagraphElement>(null)
  const [refocus, setRefocus] = useState<"more" | "end" | null>(null)

  useEffect(() => {
    if (refocus === null || audit.isFetchingNextPage) return
    const target = refocus === "more" ? moreRef.current : endRef.current
    if (target === null) return
    target.focus()
    setRefocus(null)
  }, [refocus, audit.isFetchingNextPage, audit.hasNextPage])

  if (audit.isPending) return <Spinner aria-label="Loading the audit log" />
  if (audit.data === undefined) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(audit.error)}</AlertDescription>
      </Alert>
    )
  }
  const entries = audit.data.pages.flatMap((page) => page.entries)
  if (entries.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the page's h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Audit entries</h2>
        <EmptyState title="No audit entries yet" />
      </>
    )
  }
  return (
    <div className="flex flex-col gap-4">
      <Timeline aria-label="Timeline">
        {entries.map((e) => (
          <Entry key={e.id} entry={e} />
        ))}
      </Timeline>
      {audit.isError && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{errorMessage(audit.error)}</AlertDescription>
        </Alert>
      )}
      {audit.hasNextPage ? (
        <Button
          ref={moreRef}
          variant="outline"
          className="self-start"
          loading={audit.isFetchingNextPage}
          onClick={async () => {
            const result = await audit.fetchNextPage()
            setRefocus(result.hasNextPage ? "more" : "end")
          }}
        >
          Load older entries
        </Button>
      ) : (
        <p ref={endRef} tabIndex={-1} className="text-sm text-muted-foreground">
          No older entries.
        </p>
      )}
    </div>
  )
}

export function AuditPage() {
  usePageTitle("Audit log")
  return (
    <>
      <PageHeader title="Audit log" description="Who did what, newest first." />
      <AuditBody />
    </>
  )
}
