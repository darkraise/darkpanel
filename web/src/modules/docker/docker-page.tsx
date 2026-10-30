import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ContainersTab } from "./containers-tab"
import { ImagesTab } from "./images-tab"
import { NetworksTab } from "./networks-tab"
import { ProjectsTab } from "./projects-tab"
import { VolumesTab } from "./volumes-tab"

// DockerPage is /hosts/:hostId/docker: one tab for each kind of Docker
// object on the host.
export function DockerPage({ hostId }: { hostId: string }) {
  return (
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
  )
}
