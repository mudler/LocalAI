// Shows the inbox notifications of the servers as desktop notifications.
// Imports Electron; the streams, the link check and the suppression rule are
// in notifications.ts and are tested there.
import { app, Notification } from 'electron'
import type { LauncherCore } from './core'
import { formatNotification, Notifier, shouldSuppress, type InboxItem, type NotifyTarget } from './notifications'

export interface NotifyGlueDeps {
  core(): LauncherCore | undefined
  /** The launcher window has the keyboard focus (and is on screen). */
  windowFocused(): boolean
  /** The URL the WebUI view shows, if any. */
  currentUrl(): string | undefined
  /** Shows and focuses the launcher window. */
  showWindow(): void
  /** The total unread count changed. */
  onUnread(total: number): void
  log(message: string, level?: 'info' | 'warn'): void
  signal?(line: string): void
}

export class DesktopNotifier {
  private readonly notifier: Notifier
  /** Shown notifications are kept until they close, so they are not collected before the click. */
  private readonly live = new Set<Notification>()

  constructor(private readonly d: NotifyGlueDeps) {
    this.notifier = new Notifier({
      fetch,
      setTimeout: (fn, ms) => {
        const t = setTimeout(fn, ms)
        t.unref()
        return t
      },
      clearTimeout: (t) => clearTimeout(t as NodeJS.Timeout),
      log: (m) => d.log(m),
      onNotification: (t, item) => this.show(t, item),
      onTotal: (n) => this.total(n),
    })
  }

  /** Makes the streams match the servers and the setting. Safe to call at any change. */
  sync(): void {
    const core = this.d.core()
    if (!core) return
    const { enabled, targets, all } = core.notificationTargets()
    this.notifier.setEnabled(enabled)
    this.notifier.sync(targets, all)
  }

  stop(): void {
    this.notifier.stopAll()
  }

  private total(n: number): void {
    this.d.signal?.(`unread=${n}`)
    try {
      app.setBadgeCount(n)
    } catch {
      /* not supported here */
    }
    this.d.onUnread(n)
  }

  private show(target: NotifyTarget, item: InboxItem): void {
    const core = this.d.core()
    if (!core || item.read) return
    const facts = { windowFocused: this.d.windowFocused(), webuiShown: core.webuiState().layout === 'webui', currentUrl: this.d.currentUrl() }
    if (shouldSuppress(facts, target.origin, item.link)) {
      this.d.signal?.('notification-suppressed')
      return
    }
    if (!Notification.isSupported()) return
    const shown = formatNotification(target.name, item, this.notifier.multiple)
    const n = new Notification({ title: shown.title, body: shown.body })
    this.live.add(n)
    n.on('click', () => {
      this.d.signal?.('notification-click')
      this.d.showWindow()
      if (item.link) core.openNotificationLink(target.id, item.link)
    })
    n.on('close', () => this.live.delete(n))
    n.on('failed', () => this.live.delete(n))
    n.show()
    this.d.signal?.(`notification-shown ${JSON.stringify(shown.title)}`)
  }
}
