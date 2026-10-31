import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Textarea } from "darkraise-ui/components/textarea"
import { errorMessage, readAction } from "@/api/client"
import { TailnetChange } from "./tailnet-change"
import { TAILNET_ACTION_VERSION, tailnetKey, type TailnetPolicy } from "./types"

interface PolicyDraft {
  etag: string
  text: string
}

export function PolicyPage() {
  const query = useQuery({
    queryKey: tailnetKey("policy"),
    queryFn: () => readAction<TailnetPolicy>("tailnet.policy.get", TAILNET_ACTION_VERSION),
  })
  const [draft, setDraft] = useState<PolicyDraft | null>(null)
  const [pending, setPending] = useState<string | null>(null)

  if (query.isPending) return <Spinner aria-label="Loading the policy" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  const stored = query.data
  const text = draft !== null && draft.etag === stored.etag ? draft.text : stored.text
  const unchanged = text === stored.text
  const replaced = draft !== null && draft.etag !== stored.etag && draft.text !== stored.text

  function submit(e: FormEvent) {
    e.preventDefault()
    setPending(text)
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Policy file</CardTitle>
          <CardDescription>
            The policy is HuJSON: comments are kept. Review checks the text with Tailscale and shows what changes before anything is applied.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {replaced && (
            <Alert role="status" className="mb-4">
              <AlertDescription>
                {`The policy changed on Tailscale since you began editing, so your edits were replaced by version ${stored.etag}.`}
              </AlertDescription>
              <Button type="button" variant="outline" size="sm" onClick={() => setDraft(null)}>
                Dismiss
              </Button>
            </Alert>
          )}
          <form aria-label="Policy file" className="flex flex-col gap-4" onSubmit={submit}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="pp-text">Policy file (HuJSON)</Label>
              <Textarea
                id="pp-text"
                rows={24}
                spellCheck={false}
                className="font-mono"
                value={text}
                onChange={(e) => setDraft({ etag: stored.etag, text: e.target.value })}
              />
              <p className="text-sm text-muted-foreground">{`Version ${stored.etag}`}</p>
            </div>
            <div className="flex gap-2">
              <Button type="submit" disabled={unchanged}>
                Review changes
              </Button>
              <Button type="button" variant="outline" disabled={unchanged} onClick={() => setDraft(null)}>
                Discard edits
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
      {pending !== null && (
        <TailnetChange action="tailnet.policy.set" title="Apply the policy file" input={{ text: pending }} onClose={() => setPending(null)} />
      )}
    </>
  )
}
