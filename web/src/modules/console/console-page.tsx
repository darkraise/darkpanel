import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useSearchParams } from "react-router"
import { Spinner } from "darkraise-ui/components/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { HOSTS_QUERY_KEY, listHosts } from "@/api/client"
import { FilesView } from "./files-view"
import type { OpenSurface } from "./terminal"
import { TerminalView } from "./terminal-view"

// ConsolePage is the console tab: a terminal on the host and its files
// (UI-5). The host page shows the off state while the module is off. A
// link carrying ?path= opens on the files.
export function ConsolePage({ hostId, openSurface }: { hostId: string; openSurface?: OpenSurface }) {
  const [params] = useSearchParams()
  const [tab, setTab] = useState(params.has("path") ? "files" : "terminal")
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  return (
    <div className="flex flex-col gap-4">
      <h2 className="sr-only">Console</h2>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList aria-label="Console">
          <TabsTrigger value="terminal">Terminal</TabsTrigger>
          <TabsTrigger value="files">Files</TabsTrigger>
        </TabsList>
        {/* Mounted while Files shows, so switching tabs keeps the shell. */}
        <TabsContent value="terminal" forceMount>
          {host === undefined ? (
            <Spinner aria-label="Loading the host" />
          ) : (
            <TerminalView
              hostId={hostId}
              hostName={host.name}
              online={host.online}
              reported={host.modules.some((m) => m.id === "console")}
              openSurface={openSurface}
            />
          )}
        </TabsContent>
        <TabsContent value="files">
          <FilesView hostId={hostId} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
