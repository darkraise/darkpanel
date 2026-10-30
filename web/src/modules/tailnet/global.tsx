import { NavLink, Route, Routes } from "react-router"
import { PageHeader } from "darkraise-ui/layout"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { AccountPanel } from "./account-panel"
import { DevicesPage } from "./devices-page"
import { KeysPage } from "./keys-page"
import { PolicyPage } from "./policy-page"

const LINKS = [
  { to: "/tailnet", label: "Account", end: true },
  { to: "/tailnet/devices", label: "Devices", end: false },
  { to: "/tailnet/keys", label: "Auth keys", end: false },
  { to: "/tailnet/policy", label: "Policy", end: false },
]

export default function TailnetGlobal() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Tailnet" description="The Tailscale account, its devices, auth keys and access policy." />
      <nav aria-label="Tailnet pages" className="flex gap-4">
        {LINKS.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            end={l.end}
            className={({ isActive }) => (isActive ? "font-medium underline underline-offset-4" : "text-muted-foreground underline-offset-4 hover:underline")}
          >
            {l.label}
          </NavLink>
        ))}
      </nav>
      <Routes>
        <Route index element={<AccountPanel />} />
        <Route path="devices" element={<DevicesPage />} />
        <Route path="keys" element={<KeysPage />} />
        <Route path="policy" element={<PolicyPage />} />
        <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
      </Routes>
    </div>
  )
}
