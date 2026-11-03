import { Badge } from "darkraise-ui/components/badge"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"

// MarkBadges shows who owns an object and whether Darkpanel lost track of it.
export function MarkBadges({ reserved, unclaimed }: { reserved: string; unclaimed: boolean }) {
  return (
    <span className="flex flex-wrap gap-1">
      {reserved !== "" && <Badge variant="secondary">{`Reserved: ${reserved}`}</Badge>}
      {unclaimed && <Badge variant="outline">Unclaimed Darkpanel object</Badge>}
    </span>
  )
}

export interface FieldProps {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  help?: string
  invalid?: boolean
}

// Field is a labelled text input with an optional help line.
export function Field({ id, label, value, onChange, placeholder, help, invalid }: FieldProps) {
  const helpId = `${id}-help`
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        aria-invalid={invalid === true ? true : undefined}
        aria-describedby={help !== undefined ? helpId : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {help !== undefined && (
        <p id={helpId} className="text-sm text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  )
}
