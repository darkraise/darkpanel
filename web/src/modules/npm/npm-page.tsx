import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { InstancePanel } from "./instance-panel"
import { readInstanceStatus } from "./status"
import { npmKey } from "./types"

// NpmPage is /hosts/:hostId/npm: the host's NPM instance, with a warning
// while its admin interface is exposed. A failed status read is reported by
// the Instance panel, which shares the page's query.
export function NpmPage({ hostId }: { hostId: string }) {
  const status = useQuery({ queryKey: npmKey(hostId, "instance.status"), queryFn: () => readInstanceStatus(hostId) })
  const exposure = status.data?.adminExposure
  return (
    <div className="flex flex-col gap-6">
      {exposure !== null && exposure !== undefined && (
        <Alert variant="warning" role="status">
          <AlertTitle>Admin interface exposed</AlertTitle>
          <AlertDescription>{`The NPM admin interface is published at https://${exposure.hostname}. Remove the exposure when you no longer need it.`}</AlertDescription>
        </Alert>
      )}
      <Tabs defaultValue="instance">
        <TabsList aria-label="Nginx Proxy Manager">
          <TabsTrigger value="instance">Instance</TabsTrigger>
        </TabsList>
        <TabsContent value="instance">
          <InstancePanel hostId={hostId} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
