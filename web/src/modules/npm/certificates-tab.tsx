import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Field, MarkBadges, ReadFailure } from "./list-parts"
import { readCertificateList } from "./list-replies"
import { NpmChange } from "./npm-change"
import { parseDomains } from "./parse"
import { npmKey, type CertificateView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

function certLabel(c: CertificateView): string {
  return c.name !== "" ? c.name : c.domains.join(", ")
}

function RequestForm({ onPlan }: { onPlan: (domains: string[]) => void }) {
  const [text, setText] = useState("")
  const domains = parseDomains(text)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (domains.length > 0) onPlan(domains)
  }

  return (
    <form aria-label="Certificate" className="flex flex-col gap-4" onSubmit={submit}>
      <Field id="certificate-domains" label="Domains" value={text} onChange={setText} help="Separate several names with commas or spaces." />
      <DialogFooter>
        <Button type="submit" disabled={domains.length === 0}>Plan</Button>
      </DialogFooter>
    </form>
  )
}

function CertificateTable({ items, onRemove }: { items: CertificateView[]; onRemove: (c: CertificateView) => void }) {
  if (items.length === 0) return <EmptyState title="No certificates" />
  return (
    <Table aria-label="Certificates">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Name</TableHead>
          <TableHead>Domains</TableHead>
          <TableHead>Expires</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((c) => (
          <TableRow key={c.id}>
            <TableCell>{c.id}</TableCell>
            <TableCell>{c.name}</TableCell>
            <TableCell>{c.domains.join(", ")}</TableCell>
            <TableCell>{c.expiresOn}</TableCell>
            <TableCell>
              <MarkBadges reserved={c.reserved} unclaimed={c.unclaimed} />
            </TableCell>
            <TableCell>
              <Button size="sm" variant="outline" aria-label={`Remove ${certLabel(c)}`} disabled={c.reserved !== ""} onClick={() => onRemove(c)}>
                Remove
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// CertificatesTab lists the host's NPM certificates and requests or removes
// one through the change dialog.
export function CertificatesTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [open, setOpen] = useState(false)
  const query = useQuery({
    queryKey: npmKey(hostId, "certificate.list"),
    queryFn: () => readCertificateList(hostId),
  })

  let content
  if (query.data === undefined) {
    content = query.isPending ? <Spinner aria-label="Loading certificates" /> : <ReadFailure error={query.error} />
  } else {
    content = (
      <>
        {query.isError && <ReadFailure error={query.error} />}
        <CertificateTable
          items={query.data.items}
          onRemove={(c) => setPending({ action: "npm.certificate.remove", title: `Remove certificate ${certLabel(c)}`, input: { id: c.id } })}
        />
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end">
        <Button onClick={() => setOpen(true)}>Request certificate</Button>
      </div>
      {content}
      {open && (
        <Dialog open onOpenChange={setOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Request a certificate</DialogTitle>
              <DialogDescription>The next step shows what the request changes before anything is applied.</DialogDescription>
            </DialogHeader>
            <RequestForm
              onPlan={(domains) => {
                setOpen(false)
                setPending({ action: "npm.certificate.request", title: `Request a certificate for ${domains.join(", ")}`, input: { domains } })
              }}
            />
          </DialogContent>
        </Dialog>
      )}
      <NpmChange
        hostId={hostId}
        action={pending?.action ?? "npm.certificate.request"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
