import { useState } from "react"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"

const TAG = /^tag:[a-z0-9][a-z0-9-]{0,62}$/

function parseTags(text: string): string[] {
  const tags = text
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => tag !== "")
  return tags.filter((tag, i) => tags.indexOf(tag) === i)
}

function initialText(tags: unknown): string {
  return Array.isArray(tags) && tags.every((tag) => typeof tag === "string") ? tags.join(", ") : ""
}

// TailnetHostCreate is the tailnet module's part of the Add host dialog: it
// asks for the new host to join the tailnet, with the tags of its node.
export default function TailnetHostCreate({ value, onChange }: { value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }) {
  const joined = value.tailnetJoin === true
  const [text, setText] = useState(() => initialText(value.tags))
  const invalid = parseTags(text).some((tag) => !TAG.test(tag))

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <Checkbox
          id="host-tailnet-join"
          checked={joined}
          onCheckedChange={(checked) => onChange(checked === true ? { tailnetJoin: true, tags: parseTags(text) } : {})}
        />
        <Label htmlFor="host-tailnet-join">Join the tailnet</Label>
      </div>
      {joined && (
        <>
          <div className="flex flex-col gap-2">
            <Label htmlFor="host-tailnet-tags">Tags (comma-separated)</Label>
            <Input
              id="host-tailnet-tags"
              value={text}
              placeholder="tag:server, tag:ci"
              autoComplete="off"
              aria-invalid={invalid ? true : undefined}
              aria-describedby={invalid ? "host-tailnet-tags-help" : undefined}
              onChange={(e) => {
                setText(e.target.value)
                onChange({ tailnetJoin: true, tags: parseTags(e.target.value) })
              }}
            />
            {invalid && (
              <p id="host-tailnet-tags-help" className="text-sm text-destructive">
                Each tag looks like tag:server.
              </p>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            The new host gets a one-use tailnet auth key in a bootstrap bundle and enrolls over the tailnet, so leave the controller address empty.
          </p>
        </>
      )}
    </div>
  )
}
