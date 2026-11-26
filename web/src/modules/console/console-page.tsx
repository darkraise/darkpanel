import { useQuery } from "@tanstack/react-query"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { HOSTS_QUERY_KEY, listHosts } from "@/api/client"
import type { OpenSurface } from "./terminal"
import { TerminalView } from "./terminal-view"

// ConsolePage is the console tab: a terminal on the host and its files
// (UI-5). The host page shows the off state while the module is off.
export function ConsolePage({ hostId, openSurface }: { hostId: string; openSurface?: OpenSurface }) {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  return (
    <div className="flex flex-col gap-4">
      <h2 className="sr-only">Console</h2>
      <Tabs defaultValue="terminal">
        <TabsList aria-label="Console">
          <TabsTrigger value="terminal">Terminal</TabsTrigger>
          <TabsTrigger value="files">Files</TabsTrigger>
        </TabsList>
        <TabsContent value="terminal">
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
          <EmptyState title="Files" description="This page is not built yet." />
        </TabsContent>
      </Tabs>
    </div>
  )
}
