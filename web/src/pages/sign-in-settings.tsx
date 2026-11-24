import { useState, type FormEvent, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { SIGN_IN_SETTINGS_QUERY_KEY, errorMessage, getListenerState, getSignInSettings } from "@/api/client"
import type { OIDCSettings, TailscaleSettings } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { usePageTitle } from "@/lib/page-title"
import { applied } from "@/lib/users"

const MAX_BUTTON_NAME = 32

interface Pending {
  title: string
  name: string
  input: Record<string, unknown>
  card: "oidc" | "tailscale"
}

function httpsURL(text: string): URL | null {
  try {
    const url = new URL(text)
    return url.protocol === "https:" && url.host !== "" ? url : null
  } catch {
    return null
  }
}

function Field({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error !== undefined ? (
        <p id={`${id}-note`} className="text-sm text-destructive-text">
          {error}
        </p>
      ) : (
        hint !== undefined && (
          <p id={`${id}-note`} className="text-xs text-muted-foreground">
            {hint}
          </p>
        )
      )}
    </div>
  )
}

function SwitchRow({ id, label, hint, checked, onChange }: { id: string; label: string; hint?: string; checked: boolean; onChange: (on: boolean) => void }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        <Switch id={id} checked={checked} onCheckedChange={onChange} aria-describedby={hint !== undefined ? `${id}-hint` : undefined} />
      </div>
      {hint !== undefined && (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  )
}

function CardHead({ id, title, description, state }: { id: string; title: string; description: string; state: { label: string; on: boolean } }) {
  return (
    <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
      <div className="flex flex-col gap-1">
        <CardTitle id={id}>
          <h2>{title}</h2>
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </div>
      <Badge variant={state.on ? "success" : "secondary"}>{state.label}</Badge>
    </CardHeader>
  )
}

type OIDCErrors = Partial<Record<"issuer" | "clientId" | "secret" | "displayName" | "redirectBase", string>>

function OIDCCard({ oidc, onPlan }: { oidc: OIDCSettings | null; onPlan: (p: Pending) => void }) {
  const here = typeof window !== "undefined" && window.location.protocol === "https:" ? window.location.origin : ""
  const [issuer, setIssuer] = useState(oidc?.issuer ?? "")
  const [clientId, setClientId] = useState(oidc?.clientId ?? "")
  const [secret, setSecret] = useState("")
  const [displayName, setDisplayName] = useState(oidc?.displayName ?? "")
  const [redirectBase, setRedirectBase] = useState(oidc?.redirectBase ?? here)
  const [requireTotp, setRequireTotp] = useState(oidc?.requireTotp ?? false)
  const [errors, setErrors] = useState<OIDCErrors>({})
  const secretSet = oidc?.clientSecretSet === true

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const found: OIDCErrors = {}
    if (httpsURL(issuer.trim()) === null) found.issuer = "Enter an https URL, such as https://login.example.com."
    if (clientId.trim() === "") found.clientId = "Enter the client ID the provider issued."
    if (!secretSet && secret === "") found.secret = "Enter the client secret. None is saved yet."
    const name = displayName.trim()
    if (name === "" || [...name].length > MAX_BUTTON_NAME) found.displayName = `Use 1 to ${MAX_BUTTON_NAME} characters.`
    const base = httpsURL(redirectBase.trim())
    if (base === null) found.redirectBase = "Enter the https address users open Darkpanel at."
    setErrors(found)
    if (Object.keys(found).length > 0 || base === null) return
    onPlan({
      title: "Save the OIDC settings",
      name: "auth.oidc.set",
      card: "oidc",
      input: {
        issuer: issuer.trim(),
        clientId: clientId.trim(),
        ...(secret === "" ? {} : { clientSecret: secret }),
        displayName: name,
        redirectBase: base.origin,
        requireTotp,
      },
    })
  }

  const described = (key: keyof OIDCErrors, id: string, hint: boolean) => ({
    "aria-invalid": errors[key] !== undefined,
    "aria-describedby": errors[key] !== undefined || hint ? `${id}-note` : undefined,
  })
  const buttonHint = displayName.trim() === "" ? "The text of the sign-in button on the login page." : `Shown on the login page as "Sign in with ${displayName.trim()}".`

  return (
    <Card role="region" aria-labelledby="oidc-title">
      <CardHead id="oidc-title" title="OIDC" description="Sign in through your identity provider." state={oidc === null ? { label: "Off", on: false } : { label: "Configured", on: true }} />
      <CardContent>
        <form className="flex flex-col gap-3" noValidate onSubmit={submit}>
          <Field id="oidc-issuer" label="Issuer URL" error={errors.issuer}>
            <Input id="oidc-issuer" value={issuer} autoComplete="off" placeholder="https://login.example.com" onChange={(e) => setIssuer(e.target.value)} {...described("issuer", "oidc-issuer", false)} />
          </Field>
          <Field id="oidc-client" label="Client ID" error={errors.clientId}>
            <Input id="oidc-client" value={clientId} autoComplete="off" onChange={(e) => setClientId(e.target.value)} {...described("clientId", "oidc-client", false)} />
          </Field>
          <Field id="oidc-secret" label="Client secret" hint={secretSet ? "Saved — enter a new value to replace it." : undefined} error={errors.secret}>
            <Input
              id="oidc-secret"
              type="password"
              value={secret}
              autoComplete="new-password"
              placeholder={secretSet ? "••••••••" : undefined}
              onChange={(e) => setSecret(e.target.value)}
              {...described("secret", "oidc-secret", secretSet)}
            />
          </Field>
          <Field id="oidc-name" label="Button name" hint={buttonHint} error={errors.displayName}>
            <Input id="oidc-name" value={displayName} autoComplete="off" maxLength={MAX_BUTTON_NAME} onChange={(e) => setDisplayName(e.target.value)} {...described("displayName", "oidc-name", true)} />
          </Field>
          <Field id="oidc-redirect" label="Redirect base" hint="The address users open Darkpanel at." error={errors.redirectBase}>
            <Input
              id="oidc-redirect"
              value={redirectBase}
              autoComplete="off"
              placeholder="https://panel.example.com"
              onChange={(e) => setRedirectBase(e.target.value)}
              {...described("redirectBase", "oidc-redirect", true)}
            />
          </Field>
          <SwitchRow id="oidc-totp" label="Require Darkpanel code too" hint="Ask for the authenticator code after the provider." checked={requireTotp} onChange={setRequireTotp} />
          <div className="flex flex-wrap gap-2">
            <Button type="submit">Save</Button>
            {oidc !== null && (
              <Button type="button" variant="outline" onClick={() => onPlan({ title: "Turn off OIDC sign-in", name: "auth.oidc.clear", card: "oidc", input: {} })}>
                Turn off
              </Button>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

function TailscaleCard({ tailscale, onPlan }: { tailscale: TailscaleSettings; onPlan: (p: Pending) => void }) {
  const [enabled, setEnabled] = useState(tailscale.enabled)
  const [requireTotp, setRequireTotp] = useState(tailscale.requireTotp)
  const listeners = useQuery({ queryKey: ["listeners"], queryFn: getListenerState, refetchOnWindowFocus: true })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onPlan({ title: "Save the Tailscale sign-in settings", name: "auth.tailscale.set", card: "tailscale", input: { enabled, requireTotp } })
  }

  return (
    <Card role="region" aria-labelledby="tailscale-title">
      <CardHead
        id="tailscale-title"
        title="Tailscale identity"
        description="Sign in as the tailnet user of the device you browse from."
        state={tailscale.enabled ? { label: "On", on: true } : { label: "Off", on: false }}
      />
      <CardContent>
        <form className="flex flex-col gap-3" noValidate onSubmit={submit}>
          <SwitchRow id="tailscale-on" label="Sign in with Tailscale identity" checked={enabled} onChange={setEnabled} />
          <SwitchRow id="tailscale-totp" label="Require Darkpanel code too" hint="Ask for the authenticator code after the tailnet match." checked={requireTotp} onChange={setRequireTotp} />
          <p className="text-sm text-muted-foreground">Works only when Darkpanel is opened at its tailnet address.</p>
          {listeners.data?.tailscaleBound === false && (
            <Alert variant="warning">
              <AlertDescription>Darkpanel is not listening on the tailnet now.</AlertDescription>
            </Alert>
          )}
          <div>
            <Button type="submit">Save</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

export function SignInSettingsPage() {
  usePageTitle("Sign-in")
  const client = useQueryClient()
  const settings = useQuery({ queryKey: SIGN_IN_SETTINGS_QUERY_KEY, queryFn: getSignInSettings })
  const [pending, setPending] = useState<Pending | null>(null)
  // A saved card remounts, so its fields show what was stored and a typed
  // client secret is dropped.
  const [saves, setSaves] = useState({ oidc: 0, tailscale: 0 })

  let body
  if (settings.isPending) {
    body = (
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2" aria-label="Loading the sign-in settings">
        <Skeleton className="h-96 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    )
  } else if (settings.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load the sign-in settings: ${errorMessage(settings.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void settings.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else {
    const { oidc, tailscale } = settings.data
    body = (
      <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-2">
        <OIDCCard key={`${JSON.stringify(oidc)}-${saves.oidc}`} oidc={oidc} onPlan={setPending} />
        <TailscaleCard key={`${JSON.stringify(tailscale)}-${saves.tailscale}`} tailscale={tailscale} onPlan={setPending} />
      </div>
    )
  }

  return (
    <>
      <PageHeader title="Sign-in" description="Ways to sign in besides a password and an authenticator code." />
      {body}
      {pending !== null && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : setPending(null))}
          title={pending.title}
          target={{ name: pending.name, version: 1 }}
          input={pending.input}
          onApplied={(change) => {
            if (!applied(change)) return
            setSaves((s) => ({ ...s, [pending.card]: s[pending.card] + 1 }))
            void client.invalidateQueries({ queryKey: SIGN_IN_SETTINGS_QUERY_KEY })
          }}
        />
      )}
    </>
  )
}
