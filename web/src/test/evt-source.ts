// EvtSource stands in for EventSource, which jsdom lacks: it records every
// stream opened and lets a test dispatch named events on it.
export class EvtSource extends EventTarget {
  static instances: EvtSource[] = []
  readonly url: string
  closed = false

  constructor(url: string | URL) {
    super()
    this.url = String(url)
    EvtSource.instances.push(this)
  }

  close(): void {
    this.closed = true
  }

  // emit dispatches one server-sent event of type with data as its JSON.
  emit(type: string, data: unknown): void {
    this.dispatchEvent(new MessageEvent(type, { data: typeof data === "string" ? data : JSON.stringify(data) }))
  }
}
