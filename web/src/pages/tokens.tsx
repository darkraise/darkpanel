import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { TOKENS_QUERY_KEY, errorMessage, listTokens } from "@/api/client"
import type { TokenInfo } from "@/api/types"
import { formatTime } from "@/lib/format"
import { CreateTokenDialog } from "./create-token-dialog"
import { RevokeTokenDialog } from "./revoke-token-dialog"

// tokenState judges expiry as of the moment the list was fetched.
function tokenState(t: TokenInfo, asOf: number): { text: string; variant: BadgeVariant } {
  if (t.revokedAt !== null) return { text: "Revoked", variant: "outline" }
  if (Date.parse(t.expiresAt) <= asOf) return { text: "Expired", variant: "outline" }
  return { text: "Active", variant: "default" }
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
  if (tokens.data.tokens.length === 0) return <EmptyState title="No API tokens yet" />
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
                <TableCell className="font-medium">{t.name}</TableCell>
                <TableCell>{t.scope}</TableCell>
                <TableCell>{formatTime(t.createdAt)}</TableCell>
                <TableCell>{formatTime(t.expiresAt)}</TableCell>
                <TableCell>{formatTime(t.lastUsedAt)}</TableCell>
                <TableCell>
                  <Badge variant={state.variant} size="sm">
                    {state.text}
                  </Badge>
                </TableCell>
                <TableCell>
                  {t.revokedAt === null && (
                    <Button variant="outline" size="sm" onClick={() => setRevoking(t)}>
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
  return (
    <>
      <PageHeader title="API tokens" description="Tokens let the CLI and scripts call the API." actions={<CreateTokenDialog />} />
      <TokensBody />
    </>
  )
}
