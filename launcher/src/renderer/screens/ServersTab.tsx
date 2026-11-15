// Settings > Servers (mock screens 20 to 23): the saved servers, the default,
// add, edit, test, remove, and the opt-in to keep the local server running.
import { useEffect, useId, useRef, useState } from 'react'
import { Button, Switch } from '@localai/ui-kit'
import type { ServerRowView, ServersView } from '../../shared/types'
import { useBridge } from '../bridge'
import { Dialog } from '../Dialog'
import { Popover } from '../Popover'
import { ServerForm } from '../ServerForm'
import { statusTone } from '../serversUi'
import { S } from '../strings'
import { Card } from '../parts'

type Sheet = { kind: 'add' } | { kind: 'edit'; id: string } | null
const DOT: Record<string, string> = { ok: 'dk-dot--ok', warn: 'dk-dot--warn', error: 'dk-dot--error', idle: '', accent: '' }

export function ServerDot({ row }: { row: ServerRowView }) {
  const tone = statusTone(row.status, row.insecure)
  return <span className={`dk-dot lnr-dot ${DOT[tone] ?? ''}`.trim()} data-tone={tone} aria-hidden="true" />
}

function RowMenu({ row, onEdit, onTest, onDefault, onRemove }: { row: ServerRowView; onEdit: () => void; onTest: () => void; onDefault: () => void; onRemove: () => void }) {
  const [open, setOpen] = useState(false)
  const btn = useRef<HTMLButtonElement>(null)
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const items: { label: string; run: () => void; danger?: boolean; sep?: boolean; hide?: boolean }[] = [
    { label: S.servers.menuEdit, run: onEdit },
    { label: S.servers.menuTest, run: onTest },
    { label: S.servers.menuDefault, run: onDefault, hide: row.isDefault },
    { label: S.servers.menuRemove, run: onRemove, danger: true, sep: true },
  ]
  const shown = items.filter((i) => !i.hide)
  useEffect(() => {
    if (open) refs.current[0]?.focus()
  }, [open])
  const move = (e: React.KeyboardEvent) => {
    const list = refs.current.filter(Boolean) as HTMLButtonElement[]
    const at = list.indexOf(document.activeElement as HTMLButtonElement)
    const go = (i: number) => {
      e.preventDefault()
      list[(i + list.length) % list.length]?.focus()
    }
    if (e.key === 'ArrowDown') go(at + 1)
    else if (e.key === 'ArrowUp') go(at - 1)
    else if (e.key === 'Home') go(0)
    else if (e.key === 'End') go(list.length - 1)
    else if (e.key === 'Tab') setOpen(false)
  }
  return (
    <span className="lnr-anchor">
      <Button
        ref={btn}
        size="sm"
        variant="ghost"
        icon="more"
        aria-label={S.servers.moreFor(row.name)}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        data-testid={`row-menu-${row.id}`}
      />
      <Popover open={open} onClose={() => setOpen(false)} trigger={btn} kind="menu" className="dk-menu--end lnr-menu lnr-pop--down" role="menu" label={S.servers.moreFor(row.name)} onKeyDown={move}>
        {shown.map((it, i) => (
          <div key={it.label} role="none">
            {it.sep && <hr className="dk-menu-sep" />}
            <button
              ref={(el) => {
                refs.current[i] = el
              }}
              type="button"
              role="menuitem"
              tabIndex={-1}
              className={`dk-menu-item${it.danger ? ' lnr-menu__danger' : ''}`}
              onClick={() => {
                setOpen(false)
                it.run()
                btn.current?.focus()
              }}
            >
              {it.label}
            </button>
          </div>
        ))}
      </Popover>
    </span>
  )
}

export function ServersTab({ view, onSwitch, open }: { view: ServersView; onSwitch: (id: string) => void; open?: { kind: 'add' } | { kind: 'edit'; id: string } }) {
  const b = useBridge()
  const ids = useId()
  const [sheet, setSheet] = useState<Sheet>(open ?? null)
  const [removing, setRemoving] = useState<string | null>(null)
  const [note, setNote] = useState<{ id: string; text: string } | null>(null)
  const remotes = view.rows.filter((r) => r.kind === 'remote')
  const defaultRemote = view.defaultId !== 'local'

  const test = async (row: ServerRowView) => {
    setNote({ id: row.id, text: S.servers.status.checking })
    const r = await b.testServer?.({ id: row.id })
    if (!r || !r.ok) return setNote(null)
    const res = r.result
    const text =
      res.status === 'ok' ? `${row.name}: ${S.servers.status.connected}${res.version ? ` · LocalAI ${res.version}` : ''}` :
      res.status === 'needs-signin' ? `${row.name}: ${S.servers.status['needs-signin']}` :
      res.status === 'unreachable' ? `${row.name}: ${S.testResult.unreachable[res.reason]}` :
      res.status === 'tls' ? `${row.name}: ${S.testResult.tls[res.reason]}` :
      res.status === 'not-localai' ? `${row.name}: ${S.testResult.notLocalTitle}` : ''
    setNote({ id: row.id, text })
  }

  const rowView = (r: ServerRowView) => (
    <li key={r.id} className="lnr-srow" data-testid={`server-${r.id}`} data-default={r.isDefault ? '' : undefined}>
      <ServerDot row={r} />
      <div className="lnr-srow__main">
        <div className="lnr-srow__name">
          <strong>{r.name}</strong>
          {r.isDefault && <span className="lnr-badge lnr-badge--accent">{S.servers.isDefault}</span>}
          {r.insecure && <span className="lnr-badge lnr-badge--warn">{S.servers.notEncrypted}</span>}
        </div>
        <div className="lnr-srow__host dk-mono dk-small">
          {r.kind === 'local' ? `${r.host}${r.version ? ` · LocalAI ${r.version}` : ''}` : r.insecure ? `${r.origin}` : r.host}
        </div>
      </div>
      <span className="lnr-srow__status dk-small dk-muted">{S.servers.status[r.status]}</span>
      {!r.isDefault && <Button size="sm" onClick={() => onSwitch(r.id)} aria-label={`${S.servers.makeDefault}: ${r.name}`}>{S.servers.makeDefault}</Button>}
      {r.kind === 'remote' && (
        <RowMenu
          row={r}
          onEdit={() => setSheet({ kind: 'edit', id: r.id })}
          onTest={() => void test(r)}
          onDefault={() => onSwitch(r.id)}
          onRemove={() => setRemoving(r.id)}
        />
      )}
    </li>
  )

  const editing = sheet?.kind === 'edit' ? view.rows.find((r) => r.id === sheet.id) : undefined
  const removeRow = removing ? view.rows.find((r) => r.id === removing) : undefined

  return (
    <>
      <Card className="lnr-servers">
        <div className="lnr-card__head">
          <h3 className="dk-eyebrow lnr-card__title">{S.servers.savedServers}</h3>
          <Button variant="primary" size="sm" onClick={() => setSheet({ kind: 'add' })} data-testid="add-server">{S.servers.addServer}</Button>
        </div>
        <ul className="lnr-slist" aria-label={S.servers.savedServers}>
          {view.rows.map(rowView)}
        </ul>
        {remotes.length === 0 && (
          <div className="lnr-empty">
            <strong>{S.servers.emptyTitle}</strong>
            <p className="dk-small dk-muted">{S.servers.emptyBody}</p>
            <Button onClick={() => setSheet({ kind: 'add' })}>{S.servers.addServer}</Button>
          </div>
        )}
        <div className="dk-small dk-muted lnr-srow__note" role="status" aria-live="polite" data-testid="server-note">{note?.text ?? ''}</div>
      </Card>

      {defaultRemote && (
        <Card>
          <div className="lnr-keep">
            <div className="lnr-keep__text">
              <span id={`${ids}-keep-name`} className="lnr-field__label">{S.servers.keepTitle}</span>
              <span id={`${ids}-keep-note`} className="lnr-field__note">{S.servers.keepBody}</span>
            </div>
            <Switch
              checked={view.keepLocal}
              onChange={(v) => void b.setKeepLocalRunning?.(v)}
              labelledBy={`${ids}-keep-name`}
              describedBy={`${ids}-keep-note`}
              testId="keep-local"
            />
          </div>
        </Card>
      )}
      {remotes.length > 0 && <p className="dk-small dk-muted">{S.servers.removeNote}</p>}

      {sheet && (sheet.kind === 'add' || editing) && (
        <ServerForm
          key={sheet.kind === 'edit' ? sheet.id : 'add'}
          variant="sheet"
          servers={view.rows}
          editing={editing}
          onDone={() => setSheet(null)}
          onCancel={() => setSheet(null)}
          onRemove={editing ? () => (setRemoving(editing.id), setSheet(null)) : undefined}
        />
      )}
      {removeRow && (
        <Dialog title={S.servers.removeTitle(removeRow.name)} titleId={`${ids}-rm`} role="alertdialog" onClose={() => setRemoving(null)} initialFocus="[data-cancel]" testId="remove-dialog">
          {removeRow.isDefault ? (
            <>
              <p className="lnr-dialog__p">{S.servers.removeDefaultLead}</p>
              <ol className="lnr-steps lnr-dialog__steps">
                {[S.servers.removeDefault1, S.servers.removeDefault2].map((t, i) => (
                  <li key={t} className="lnr-step"><div className="lnr-step__row"><span className="lnr-step__mark lnr-step__mark--active" aria-hidden="true">{i + 1}</span><span>{t}</span></div></li>
                ))}
              </ol>
            </>
          ) : (
            <p className="lnr-dialog__p">{S.servers.removeOther}</p>
          )}
          <p className="dk-small dk-muted">{S.servers.removeFoot}</p>
          <div className="lnr-row lnr-dialog__actions">
            <span className="lnr-grow" />
            <Button data-cancel onClick={() => setRemoving(null)}>{S.servers.cancel}</Button>
            <Button
              variant="danger"
              onClick={async () => {
                const id = removeRow.id
                setRemoving(null)
                await b.removeServer?.(id)
              }}
              data-testid="remove-confirm"
            >
              {removeRow.isDefault ? S.servers.removeDefaultAction : S.servers.removeAction}
            </Button>
          </div>
        </Dialog>
      )}
    </>
  )
}
