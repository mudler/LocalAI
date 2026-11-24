// Decides whether an IPC message may reach a handler. No Electron import.
//
// Only the shell view's main frame gets the preload bridge, so only it may
// call the main process. Everything else is refused before any handler runs:
// a subframe of the shell (an iframe would get no bridge, but a compromised
// renderer could still try), the WebUI view (a different WebContents with no
// preload), a devtools page, or the shell after it navigated away from the
// app's own page.

/** What index.ts reads from an Electron IPC event. Plain data, so tests use fakes. */
export interface FrameInfo {
  /** `event.sender.id`. */
  webContentsId: number
  /** `event.senderFrame.processId`. */
  processId: number
  /** `event.senderFrame.routingId`. */
  routingId: number
  /** `event.senderFrame.url`. */
  url: string
  /** `event.senderFrame.parent === null`. */
  isMainFrame: boolean
}

/** The shell view as it is now (read live, because a reload gives the frame a new id). */
export interface TrustedShell {
  webContentsId: number
  /** `webContents.mainFrame.processId`. */
  processId: number
  /** `webContents.mainFrame.routingId`. */
  routingId: number
  /** The `file:` URL of the shell's index.html. */
  pageUrl: string
}

/** The URL without query and fragment, so `index.html#/logs` still matches. */
function pagePart(url: string): string | undefined {
  try {
    const u = new URL(url)
    return `${u.protocol}//${u.host}${u.pathname}`
  } catch {
    return undefined
  }
}

export function isTrustedSender(frame: FrameInfo | null | undefined, shell: TrustedShell | undefined): boolean {
  if (!frame || !shell) return false
  if (!frame.isMainFrame) return false
  if (frame.webContentsId !== shell.webContentsId) return false
  if (frame.processId !== shell.processId || frame.routingId !== shell.routingId) return false
  const page = pagePart(frame.url)
  return page !== undefined && page === pagePart(shell.pageUrl)
}

export class UntrustedSenderError extends Error {
  constructor() {
    super('IPC refused: the sender is not the launcher window')
    this.name = 'UntrustedSenderError'
  }
}

/**
 * Wraps a handler so the trust check runs first. `frameOf` turns the Electron
 * event into a FrameInfo; `shell` returns the current shell view, or undefined
 * when there is none.
 */
export function guard<E, R>(
  handler: (...args: unknown[]) => R,
  frameOf: (event: E) => FrameInfo | null | undefined,
  shell: () => TrustedShell | undefined,
  onRefused: (frame: FrameInfo | null | undefined) => void = () => undefined,
): (event: E, ...args: unknown[]) => R {
  return (event, ...args) => {
    const frame = frameOf(event)
    if (!isTrustedSender(frame, shell())) {
      onRefused(frame)
      throw new UntrustedSenderError()
    }
    return handler(...args)
  }
}
