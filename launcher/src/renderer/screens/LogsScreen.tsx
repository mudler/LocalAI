import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Page, SearchField, Segmented, Switch } from '@localai/ui-kit'
import type { LogLevelName, LogLineView } from '../../shared/types'
import { useBridge } from '../bridge'
import { CopyButton } from '../parts'
import { S } from '../strings'
import { clockTime } from '../viewModel'

const LEVELS: LogLevelName[] = ['error', 'warn', 'info', 'debug']
const RANK: Record<LogLevelName, number> = { debug: 0, info: 1, warn: 2, error: 3 }
const TAG: Record<LogLevelName, string> = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' }
/** Lines kept on screen; the full log is in the export. */
const KEEP = 2000

/** Lines at or above the level that contain the filter text (case does not matter). */
export function visibleLines(lines: LogLineView[], minLevel: LogLevelName, filter: string): LogLineView[] {
  const f = filter.trim().toLowerCase()
  return lines.filter((l) => RANK[l.level] >= RANK[minLevel] && (!f || l.text.toLowerCase().includes(f)))
}

export function logText(lines: LogLineView[]): string {
  return lines.map((l) => `${clockTime(l.ts, true)} ${TAG[l.level]} ${l.text}`).join('\n')
}

export function LogsScreen() {
  const b = useBridge()
  const [lines, setLines] = useState<LogLineView[]>([])
  const [level, setLevel] = useState<LogLevelName>('info')
  const [filter, setFilter] = useState('')
  const [follow, setFollow] = useState(true)
  const [exported, setExported] = useState<string>()
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let live = true
    void b.queryLogs({ limit: KEEP }).then((l) => live && setLines(l)).catch(() => undefined)
    const off = b.onLogs((more) => setLines((cur) => [...cur, ...more].slice(-KEEP)))
    return () => {
      live = false
      off()
    }
  }, [b])

  const shown = useMemo(() => visibleLines(lines, level, filter), [lines, level, filter])

  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [shown, follow])

  const exportLogs = async () => {
    try {
      const r = await b.exportLogs()
      setExported(r.status === 'saved' ? S.logs.exported(r.fileName) : undefined)
    } catch {
      setExported(S.logs.exportFailed)
    }
  }

  return (
    <Page className="lnr-screen lnr-logs" data-screen="logs">
      <div className="lnr-row lnr-logs__bar">
        <h1 className="lnr-h lnr-grow">{S.logs.title}</h1>
        <Segmented label={S.logs.levels} value={level} onChange={(v) => setLevel(v as LogLevelName)} options={LEVELS.map((l) => ({ id: l, label: S.logs.level[l] }))} />
        <SearchField className="lnr-logs__filter" label={S.logs.filter} placeholder={S.logs.filterPlaceholder} value={filter} onChange={setFilter} />
        <CopyButton label={S.logs.copy} copied={S.logs.copied} onCopy={() => b.copyText(logText(shown))} />
        <Button size="sm" icon="export" onClick={exportLogs}>{S.logs.export}</Button>
      </div>
      <div ref={box} className="lnr-log dk-mono" role="log" aria-label={S.logs.box} tabIndex={0}>
        {shown.length === 0 ? (
          <div className="dk-muted">{lines.length === 0 ? S.logs.empty : S.logs.noMatch}</div>
        ) : (
          shown.map((l) => (
            <div key={l.seq} className={`lnr-log__line lnr-log__line--${l.level}`}>
              <span className="lnr-log__t">{clockTime(l.ts, true)}</span> {TAG[l.level]} {l.text}
            </div>
          ))
        )}
      </div>
      <div className="lnr-row dk-small dk-muted lnr-logs__foot">
        <Switch checked={follow} onChange={setFollow} labelledBy="lnr-follow" />
        <span id="lnr-follow">{S.logs.autoScroll}</span>
        <span className="lnr-grow" />
        {exported && <span role="status">{exported}</span>}
      </div>
    </Page>
  )
}
