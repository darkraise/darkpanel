import { Route, Routes } from "react-router"
import { PageHeader } from "darkraise-ui/layout"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { DerpPage } from "./derp-page"

export default function DerpGlobal() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="DERP" description="Custom DERP servers and the regions that publish them in the tailnet policy." />
      <Routes>
        <Route index element={<DerpPage />} />
        <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
      </Routes>
    </div>
  )
}
