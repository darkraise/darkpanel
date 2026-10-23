// EvtSource stands in for EventSource, which jsdom lacks: it records every
// stream opened and lets a test dispatch named events on it.
export class EvtSource extends EventTarget {
  static instances: EvtSource[] = []
  readonly url: string
  closed = false
  readyState = 0

  constructor(url: string | URL) {
    super()
    this.url = String(url)
    EvtSource.instances.push(this)
  }

  close(): void {
    this.closed = true
  }

  // fail ends the stream the way a refused reconnect does: closed for good.
  fail(): void {
    this.readyState = 2
    this.dispatchEvent(new Event("error"))
  }

  // emit dispatches one server-sent event of type with data as its JSON.
  emit(type: string, data: unknown): void {
    this.dispatchEvent(new MessageEvent(type, { data: typeof data === "string" ? data : JSON.stringify(data) }))
  }
}
