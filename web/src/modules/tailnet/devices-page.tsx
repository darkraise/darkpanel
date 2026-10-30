import { useId, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ApiError, errorMessage, readAction } from "@/api/client"
import { formatTime } from "@/lib/format"
import { TailnetChange } from "./tailnet-change"
import { TAILNET_ACTION_VERSION, tailnetKey, type TailnetDevice } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

const EXIT_ROUTES = ["0.0.0.0/0", "::/0"]

function plainRoutes(routes: string[]): string[] {
  return routes.filter((r) => !EXIT_ROUTES.includes(r))
}

function routesSummary(d: TailnetDevice): string {
  const advertised = plainRoutes(d.advertisedRoutes)
  const parts: string[] = []
  if (advertised.length > 0) {
    parts.push(`${advertised.filter((r) => d.enabledRoutes.includes(r)).length} of ${advertised.length} approved`)
  }
  if (d.exitNode) parts.push(d.exitNodeEnabled ? "exit node approved" : "exit node not approved")
  return parts.length === 0 ? "none" : parts.join(", ")
}

function RouteOption({ id, label, checked, mono, onChange }: {
  id: string
  label: string
  checked: boolean
  mono?: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(c) => onChange(c === true)} />
      <Label htmlFor={id} className={mono ? "font-mono" : undefined}>
        {label}
      </Label>
    </div>
  )
}

// RoutesDialog edits which of a device's advertised routes are approved. It
// mounts only while a device is chosen, so its ticks start from the device
// each time.
function RoutesDialog({ device, onCancel, onReview }: { device: TailnetDevice; onCancel: () => void; onReview: (routes: string[]) => void }) {
  const base = useId()
  const advertised = plainRoutes(device.advertisedRoutes)
  const [ticked, setTicked] = useState<string[]>(() => advertised.filter((r) => device.enabledRoutes.includes(r)))
  const [exit, setExit] = useState(device.exitNodeEnabled)

  function toggle(route: string, on: boolean) {
    setTicked((cur) => (on ? [...cur, route] : cur.filter((r) => r !== route)))
  }

  function review() {
    onReview([...advertised.filter((r) => ticked.includes(r)), ...(device.exitNode && exit ? EXIT_ROUTES : [])])
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Routes of ${device.name}`}</DialogTitle>
          <DialogDescription>Tick the routes this device may advertise to the tailnet.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          {advertised.map((route, i) => (
            <RouteOption key={route} id={`${base}-r${i}`} label={route} mono checked={ticked.includes(route)} onChange={(on) => toggle(route, on)} />
          ))}
          {device.exitNode && (
            <RouteOption id={`${base}-exit`} label="Exit node (0.0.0.0/0 and ::/0)" checked={exit} onChange={setExit} />
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button onClick={review}>Review</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function NotConnected() {
  return (
    <Alert role="status">
      <AlertDescription>
        No tailnet account is connected. <Link to="/tailnet">Connect an account</Link> first.
      </AlertDescription>
    </Alert>
  )
}

// DevicesPage lists the tailnet's devices and plans authorizing, approving
// routes for, expiring and deleting each through the change dialog.
export function DevicesPage() {
  const [pending, setPending] = useState<Pending | null>(null)
  const [routesOf, setRoutesOf] = useState<TailnetDevice | null>(null)
  const query = useQuery({
    queryKey: tailnetKey("devices"),
    queryFn: () => readAction<{ devices: TailnetDevice[] }>("tailnet.device.list", TAILNET_ACTION_VERSION, {}),
  })
  if (query.isPending) return <Spinner aria-label="Loading the devices" />
  if (query.isError) {
    if (query.error instanceof ApiError && query.error.code === "cloud.not_connected") return <NotConnected />
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  const devices = query.data.devices

  function plan(action: string, verb: string, d: TailnetDevice) {
    setPending({ action, title: `${verb} ${d.name}`, input: { device: d.id } })
  }

  return (
    <>
      {devices.length === 0 ? (
        <EmptyState title="No devices" description="No device has joined this tailnet." />
      ) : (
        <Table aria-label="Tailnet devices">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Addresses</TableHead>
              <TableHead>OS</TableHead>
              <TableHead>Tags</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Routes</TableHead>
              <TableHead>Last seen</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {devices.map((d) => (
              <TableRow key={d.id}>
                <TableCell className="font-medium">{d.name}</TableCell>
                <TableCell className="font-mono">{d.addresses.join(", ")}</TableCell>
                <TableCell>{d.os}</TableCell>
                <TableCell>{d.tags.join(", ")}</TableCell>
                <TableCell>
                  {d.authorized ? <Badge size="sm">Authorized</Badge> : <Badge variant="secondary" size="sm">Pending approval</Badge>}
                </TableCell>
                <TableCell>{routesSummary(d)}</TableCell>
                <TableCell>{formatTime(d.lastSeen ?? null)}</TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-2">
                    {!d.authorized && (
                      <Button variant="outline" size="sm" aria-label={`Authorize ${d.name}`} onClick={() => plan("tailnet.device.authorize", "Authorize", d)}>
                        Authorize
                      </Button>
                    )}
                    {d.advertisedRoutes.length > 0 && (
                      <Button variant="outline" size="sm" aria-label={`Routes of ${d.name}`} onClick={() => setRoutesOf(d)}>
                        Routes
                      </Button>
                    )}
                    <Button variant="outline" size="sm" aria-label={`Expire ${d.name}`} onClick={() => plan("tailnet.device.expire", "Expire", d)}>
                      Expire
                    </Button>
                    <Button variant="destructive" size="sm" aria-label={`Delete ${d.name}`} onClick={() => plan("tailnet.device.delete", "Delete", d)}>
                      Delete
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {routesOf !== null && (
        <RoutesDialog
          device={routesOf}
          onCancel={() => setRoutesOf(null)}
          onReview={(routes) => {
            setPending({ action: "tailnet.device.set-routes", title: `Approve routes for ${routesOf.name}`, input: { device: routesOf.id, routes } })
            setRoutesOf(null)
          }}
        />
      )}
      {pending !== null && (
        <TailnetChange action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />
      )}
    </>
  )
}
