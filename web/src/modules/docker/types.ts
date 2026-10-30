export const DOCKER_MODULE = "docker"
export const DOCKER_ACTION_VERSION = 1

export function dockerKey(hostId: string, resource: "engine" | "containers" | "images" | "networks" | "volumes" | "projects"): string[] {
  return ["resource", hostId, "docker." + resource]
}

export interface DockerEngine {
  available: boolean
  reason?: string
  version?: string
  apiVersion?: string
  os?: string
  arch?: string
  containers: number
  running: number
  images: number
}

export interface DockerPort {
  ip?: string
  privatePort: number
  publicPort?: number
  proto: string
}

export interface DockerUsage {
  cpuPercent: number
  memUsage: number
  memLimit: number
}

export interface DockerContainer {
  id: string
  name: string
  image: string
  imageId: string
  state: string
  status: string
  created: string
  ports: DockerPort[]
  labels?: Record<string, string>
  project?: string
  service?: string
  owner?: string
  usage?: DockerUsage
}

export interface DockerImage {
  id: string
  tags: string[]
  digests: string[]
  size: number
  created: string
  containers: string[]
  dangling: boolean
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  ipv6: boolean
  subnets: string[]
  containers: string[]
  owner?: string
  created: string
}

export interface DockerVolume {
  name: string
  driver: string
  mountpoint: string
  created: string
  labels?: Record<string, string>
  containers: string[]
  owner?: string
}

export interface DockerProject {
  name: string
  directory: string
  files: string[]
  envFiles: string[]
  profiles: string[]
  configHash: string
  hashChanged: boolean
  containers: string[]
  running: number
  firstSeen: string
  lastRun?: string
}

export interface ContainerState {
  id: string
  name: string
  running: boolean
}

export interface EnvVar {
  name: string
  value: string
}

export interface DockerMount {
  type: string
  name?: string
  source: string
  destination: string
  rw: boolean
}

export interface EndpointView {
  network: string
  networkId: string
  container: string
  endpointId?: string
  aliases?: string[]
  ipv4?: string
  ipv6?: string
}

export interface ContainerDetail {
  id: string
  name: string
  image: string
  imageId: string
  created: string
  state: string
  running: boolean
  startedAt?: string
  finishedAt?: string
  exitCode: number
  restartPolicy?: string
  cmd?: string[]
  entrypoint?: string[]
  workingDir?: string
  user?: string
  env: EnvVar[]
  labels?: Record<string, string>
  ports: DockerPort[]
  mounts: DockerMount[]
  networks: EndpointView[]
  project?: string
  service?: string
  owner?: string
}

export interface LogLine {
  stream: "stdout" | "stderr"
  text: string
}

export interface LogEvent {
  stream: "stdout" | "stderr"
  text: string
}

export interface StatsEvent {
  read: string
  cpuPercent: number
  memUsage: number
  memLimit: number
  netRx: number
  netTx: number
  pids: number
}

export interface PullEvent {
  status: string
  id?: string
  progress?: string
  current?: number
  total?: number
  error?: string
}

export interface Pruned {
  images: { id: string; tags: string[]; size: number }[]
  reclaimed: number
}
