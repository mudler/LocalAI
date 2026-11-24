// Watches the remote server that is the default. No Electron import.
//
// While the server does not answer, the window shows the recovery screen
// and the launcher tries again every 30 seconds. It never starts the local
// server on its own: switching is the user's choice. While the server
// answers, nothing is polled.
import type { RemoteStatusView } from '../shared/types'
import type { TestResult } from './testConnection'

export const REMOTE_RETRY_MS = 30_000

/** A short plain reason for the recovery screen. */
export function reasonText(r: TestResult): string {
  switch (r.status) {
    case 'unreachable':
      return { timeout: 'timed out', refused: 'the connection was refused', dns: 'the name was not found', network: 'the network does not answer' }[r.reason]
    case 'tls':
      return { expired: 'the certificate has expired', 'not-yet-valid': 'the certificate is not valid yet', 'self-signed': 'the certificate is self-signed', 'name-mismatch': 'the certificate is for another name', untrusted: 'the certificate is not trusted' }[r.reason]
    case 'not-localai':
      return 'it is not a LocalAI server'
    default:
      return 'it did not answer'
  }
}

export interface MonitorDeps {
  probe(origin: string): Promise<TestResult>
  onStatus(s: RemoteStatusView | null): void
  now(): number
  retryMs?: number
}

export class RemoteMonitor {
  private gen = 0
  private origin: string | undefined
  private timer: NodeJS.Timeout | undefined

  constructor(private readonly d: MonitorDeps) {}

  /** Watch `origin`: one try now, then every 30 seconds until it answers. */
  async start(origin: string): Promise<void> {
    this.stop()
    this.origin = origin
    await this.tick(this.gen)
  }

  stop(): void {
    this.gen++
    this.origin = undefined
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
  }

  async retryNow(): Promise<void> {
    if (!this.origin) return
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
    await this.tick(this.gen)
  }

  private async tick(gen: number): Promise<void> {
    const origin = this.origin
    if (!origin) return
    const r = await this.d.probe(origin)
    if (gen !== this.gen || r.status === 'cancelled') return
    if (r.status === 'ok' || r.status === 'needs-signin') {
      this.d.onStatus(null)
      return
    }
    const at = this.d.now()
    const wait = this.d.retryMs ?? REMOTE_RETRY_MS
    this.d.onStatus({ kind: 'unreachable', reason: reasonText(r), at, nextAt: at + wait })
    this.timer = setTimeout(() => void this.tick(gen), wait)
    this.timer.unref?.()
  }
}
