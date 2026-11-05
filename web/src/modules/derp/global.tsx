import { Route, Routes } from "react-router"
import { PageHeader } from "darkraise-ui/layout"
import { PageNotFound } from "@/components/page-not-found"
import { usePageTitle } from "@/lib/page-title"
import { DerpPage } from "./derp-page"

export default function DerpGlobal() {
  usePageTitle("DERP")
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="DERP" description="Custom DERP servers and the regions that publish them in the tailnet policy." />
      <Routes>
        <Route index element={<DerpPage />} />
        <Route path="*" element={<PageNotFound />} />
      </Routes>
    </div>
  )
}
