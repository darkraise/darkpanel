import type { ContainerDetail, DockerContainer, DockerEngine, DockerImage, DockerNetwork, DockerProject, DockerVolume } from "./types"

// Test fixtures of the Docker pages: one running web container and the
// image, network, volume and project it belongs to, with the fields each
// test overrides.

const IMAGE_ID = "sha256:" + "e3b0c442".repeat(8)

export function dkEngine(over: Partial<DockerEngine> = {}): DockerEngine {
  return { available: true, version: "29.9.1", apiVersion: "1.54", os: "linux", arch: "amd64", containers: 3, running: 2, images: 4, ...over }
}

export function dkContainer(over: Partial<DockerContainer> = {}): DockerContainer {
  return {
    id: "a1b2c3d4".repeat(8),
    name: "web",
    image: "nginx:1.27",
    imageId: IMAGE_ID,
    state: "running",
    status: "Up 2 hours",
    created: "2026-10-01T00:00:00Z",
    ports: [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }],
    usage: { cpuPercent: 1.5, memUsage: 52428800, memLimit: 1073741824 },
    ...over,
  }
}

export function dkImage(over: Partial<DockerImage> = {}): DockerImage {
  return {
    id: IMAGE_ID,
    tags: ["nginx:1.27"],
    digests: ["nginx@sha256:" + "d".repeat(64)],
    size: 192000000,
    created: "2026-09-20T00:00:00Z",
    containers: ["web"],
    dangling: false,
    ...over,
  }
}

export function dkNetwork(over: Partial<DockerNetwork> = {}): DockerNetwork {
  return {
    id: "n".repeat(64),
    name: "bridge",
    driver: "bridge",
    scope: "local",
    internal: false,
    ipv6: false,
    subnets: ["172.17.0.0/16"],
    containers: ["web"],
    created: "2026-09-01T00:00:00Z",
    ...over,
  }
}

export function dkVolume(over: Partial<DockerVolume> = {}): DockerVolume {
  return {
    name: "data",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/data/_data",
    created: "2026-09-15T00:00:00Z",
    containers: ["web"],
    ...over,
  }
}

export function dkProject(over: Partial<DockerProject> = {}): DockerProject {
  return {
    name: "shop",
    directory: "/srv/shop",
    files: ["/srv/shop/compose.yaml"],
    envFiles: [],
    profiles: [],
    configHash: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
    hashChanged: false,
    containers: ["shop-web-1"],
    running: 1,
    firstSeen: "2026-10-02T00:00:00Z",
    ...over,
  }
}

export function dkDetail(over: Partial<ContainerDetail> = {}): ContainerDetail {
  const c = dkContainer()
  const volume = dkVolume()
  const network = dkNetwork()
  return {
    id: c.id,
    name: c.name,
    image: c.image,
    imageId: c.imageId,
    created: c.created,
    state: c.state,
    running: true,
    startedAt: "2026-10-01T00:00:05Z",
    exitCode: 0,
    restartPolicy: "unless-stopped",
    cmd: ["nginx", "-g", "daemon off;"],
    entrypoint: ["/docker-entrypoint.sh"],
    workingDir: "/",
    env: [
      { name: "NGINX_VERSION", value: "[redacted]" },
      { name: "TOKEN", value: "[redacted]" },
    ],
    labels: { "com.docker.compose.project": "shop", "com.docker.compose.service": "web" },
    ports: c.ports,
    mounts: [{ type: "volume", name: volume.name, source: volume.mountpoint, destination: "/data", rw: true }],
    networks: [{ network: network.name, networkId: network.id, container: c.name, endpointId: "e".repeat(64), ipv4: "172.17.0.2" }],
    ...over,
  }
}
