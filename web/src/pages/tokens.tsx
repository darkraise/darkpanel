import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Ban, Eye, KeyRound, ShieldCheck, Wrench, type LucideIcon } from "lucide-react"
import { TOKENS_QUERY_KEY, errorMessage, listTokens } from "@/api/client"
import type { TokenInfo } from "@/api/types"
import { StatusDot } from "@/components/visual/status-dot"
import type { Status } from "@/components/visual/status"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"
import { CreateTokenDialog } from "./create-token-dialog"
import { RevokeTokenDialog } from "./revoke-token-dialog"

// tokenState judges expiry as of the moment the list was fetched.
function tokenState(t: TokenInfo, asOf: number): { text: string; status: Status } {
  if (t.revokedAt !== null) return { text: "Revoked", status: "off" }
  if (Date.parse(t.expiresAt) <= asOf) return { text: "Expired", status: "warn" }
  return { text: "Active", status: "ok" }
}

const SCOPE_ICON: Record<string, LucideIcon> = { read: Eye, operate: Wrench, admin: ShieldCheck }

function ScopeChip({ scope }: { scope: string }) {
  const Icon = SCOPE_ICON[scope] ?? KeyRound
  return (
    <Badge variant="outline" role="img" aria-label={`Scope: ${scope}`} className="gap-1">
      <Icon aria-hidden className="size-3.5" />
      {scope}
    </Badge>
  )
}

function TokensBody() {
  const tokens = useQuery({ queryKey: TOKENS_QUERY_KEY, queryFn: listTokens })
  const [revoking, setRevoking] = useState<TokenInfo | null>(null)

  if (tokens.isPending) return <Spinner aria-label="Loading the tokens" />
  if (tokens.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(tokens.error)}</AlertDescription>
      </Alert>
    )
  }
  if (tokens.data.tokens.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the page's h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Token list</h2>
        <EmptyState title="No API tokens yet" />
      </>
    )
  }
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Scope</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Expires</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead>State</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tokens.data.tokens.map((t) => {
            const state = tokenState(t, tokens.dataUpdatedAt)
            return (
              <TableRow key={t.id}>
                <TableCell className="min-w-32 font-medium whitespace-normal wrap-anywhere">{t.name}</TableCell>
                <TableCell>
                  <ScopeChip scope={t.scope} />
                </TableCell>
                <TableCell>{formatTime(t.createdAt)}</TableCell>
                <TableCell>{formatTime(t.expiresAt)}</TableCell>
                <TableCell>{formatTime(t.lastUsedAt)}</TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5">
                    <StatusDot status={state.status} label={state.text} />
                    <span aria-hidden>{state.text}</span>
                  </span>
                </TableCell>
                <TableCell>
                  {t.revokedAt === null && (
                    <Button variant="outline" size="sm" aria-label={`Revoke token ${t.name}`} onClick={() => setRevoking(t)}>
                      <Ban aria-hidden />
                      Revoke
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <RevokeTokenDialog token={revoking} onClose={() => setRevoking(null)} />
    </>
  )
}

export function TokensPage() {
  usePageTitle("API tokens")
  return (
    <>
      <PageHeader title="API tokens" description="Tokens let the CLI and scripts call the API." actions={<CreateTokenDialog />} />
      <TokensBody />
    </>
  )
}
