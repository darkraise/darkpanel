import { useInfiniteQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import type { AuditList } from "@/api/types"
import { formatTime } from "@/lib/format"

const PAGE_SIZE = 50

function AuditBody() {
  const audit = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }) =>
      readAction<AuditList>("core.audit.list", 1, pageParam === null ? { limit: PAGE_SIZE } : { limit: PAGE_SIZE, before: pageParam }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextBefore,
  })

  if (audit.isPending) return <Spinner aria-label="Loading the audit log" />
  if (audit.data === undefined) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(audit.error)}</AlertDescription>
      </Alert>
    )
  }
  const entries = audit.data.pages.flatMap((page) => page.entries)
  if (entries.length === 0) return <EmptyState title="No audit entries yet" />
  return (
    <div className="flex flex-col gap-4">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Actor</TableHead>
            <TableHead>Host</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Result</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((e) => (
            <TableRow key={e.id}>
              <TableCell>{formatTime(e.at)}</TableCell>
              <TableCell className="font-mono">{e.actor}</TableCell>
              <TableCell className="font-mono">{e.hostId ?? "—"}</TableCell>
              <TableCell>
                <span className="font-mono">{`${e.action}/v${e.version}`}</span>{" "}
                <span className="text-muted-foreground">{e.phase}</span>
              </TableCell>
              <TableCell>
                {e.result}
                {e.errorCode !== null && (
                  <>
                    {" "}
                    <span className="font-mono text-muted-foreground">{e.errorCode}</span>
                  </>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {audit.isError && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{errorMessage(audit.error)}</AlertDescription>
        </Alert>
      )}
      {audit.hasNextPage && (
        <Button variant="outline" className="self-start" loading={audit.isFetchingNextPage} onClick={() => void audit.fetchNextPage()}>
          Load older entries
        </Button>
      )}
    </div>
  )
}

export function AuditPage() {
  return (
    <>
      <PageHeader title="Audit log" description="Who did what, newest first." />
      <AuditBody />
    </>
  )
}
