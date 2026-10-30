import { NavLink, Route, Routes } from "react-router"
import { PageHeader } from "darkraise-ui/layout"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { AccountPanel } from "./account-panel"
import { HostnamesPage } from "./hostnames-page"
import { TunnelsPage } from "./tunnels-page"

// The targets are absolute: inside the splat route a relative link resolves against the open page.
const LINKS = [
  { to: "/cloudflare", label: "Account", end: true },
  { to: "/cloudflare/tunnels", label: "Tunnels", end: false },
  { to: "/cloudflare/hostnames", label: "Hostnames", end: false },
]

export default function CloudflareGlobal() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Cloudflare" description="The Cloudflare account, its tunnels and the hostnames they serve." />
      <nav aria-label="Cloudflare pages" className="flex gap-4">
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
        <Route path="tunnels" element={<TunnelsPage />} />
        <Route path="hostnames" element={<HostnamesPage />} />
        <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
      </Routes>
    </div>
  )
}
