import { useQuery } from "@tanstack/react-query"
import { Card, CardContent } from "darkraise-ui/components/card"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { Container } from "lucide-react"
import { readResource } from "@/api/client"
import { SectionHeader } from "@/components/visual/section-header"
import { StatusDonut } from "@/components/visual/status-donut"
import { ContainersTab } from "./containers-tab"
import { ImagesTab } from "./images-tab"
import { NetworksTab } from "./networks-tab"
import { ProjectsTab } from "./projects-tab"
import { DOCKER_MODULE, containerSegments, dockerKey, type DockerContainer } from "./types"
import { VolumesTab } from "./volumes-tab"

// DockerPage is /hosts/:hostId/docker: one tab for each kind of Docker
// object on the host.
export function DockerPage({ hostId }: { hostId: string }) {
  const containers = useQuery({
    queryKey: dockerKey(hostId, "containers"),
    queryFn: () => readResource<{ containers: DockerContainer[] }>(hostId, DOCKER_MODULE, "containers"),
  })
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader icon={Container} title="Docker" status={containers.isSuccess ? { status: "ok", label: "Running" } : undefined} />
      {containers.data !== undefined && (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
          <Card role="region" aria-label="Containers summary">
            <CardContent className="p-4">
              <StatusDonut label="Containers" segments={containerSegments(containers.data.containers)} />
            </CardContent>
          </Card>
        </div>
      )}
      <Tabs defaultValue="containers">
        <TabsList aria-label="Docker">
          <TabsTrigger value="containers">Containers</TabsTrigger>
          <TabsTrigger value="images">Images</TabsTrigger>
          <TabsTrigger value="networks">Networks</TabsTrigger>
          <TabsTrigger value="volumes">Volumes</TabsTrigger>
          <TabsTrigger value="projects">Projects</TabsTrigger>
        </TabsList>
        <TabsContent value="containers">
          <ContainersTab hostId={hostId} />
        </TabsContent>
        <TabsContent value="images">
          <ImagesTab hostId={hostId} />
        </TabsContent>
        <TabsContent value="networks">
          <NetworksTab hostId={hostId} />
        </TabsContent>
        <TabsContent value="volumes">
          <VolumesTab hostId={hostId} />
        </TabsContent>
        <TabsContent value="projects">
          <ProjectsTab hostId={hostId} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
