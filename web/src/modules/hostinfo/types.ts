// The output of the hostinfo.summary resource (schemas/hostinfo.summary).
export interface Summary {
  hostname: string
  os: { id: string; versionId: string; prettyName: string }
  kernel: string
  arch: string
  uptimeSeconds: number
  bootTime: string
  cpu: { model: string; logicalCount: number }
  memory: { totalBytes: number; availableBytes: number }
  swap: { totalBytes: number; freeBytes: number }
  load: { one: number; five: number; fifteen: number }
  rootFs: { totalBytes: number; availableBytes: number }
  darkpanelVersion: string
}
