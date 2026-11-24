// The CSV export of one table. The dialog asks for the file of the view the
// person is looking at and then shows the host's own card for it: name, size,
// expiry and the authenticated download are the host's, drawn from its record
// of the file. This module never builds a link and never sees a path.
import { exportOutcome } from './lib/export.js'

export function createExport(ctx, ui, api, hooks) {
  const { sdk, h, R, t } = ctx
  const { Root, Banner, Problem, Loading, Button } = ui

  function ExportDialog({ db, table, args, filtered, close, onLost }) {
    const [phase, setPhase] = R.useState('idle')
    const [file, setFile] = R.useState('')
    const [error, setError] = R.useState(null)
    const [rowId, setRowId] = R.useState(false)
    const op = hooks.useOperation()

    const start = async (e) => {
      if (e) e.preventDefault()
      if (phase === 'working') return
      setPhase('working')
      setError(null)
      try {
        const body = rowId ? { ...args, columns: ['_id', ...table.columns.map(c => c.name)] } : args
        const out = exportOutcome(await api.exportCsv(db.id, body, { operationId: op.current() }))
        op.settle()
        if (out.kind === 'file') { setFile(out.id); setPhase('done') } else setPhase(out.kind)
      } catch (err) {
        // A refusal can mean the person lost access to the database or that
        // the host would not make another file (their quota is full). The
        // database answers which: when it can still be read, the page stays.
        if (err && err.kind === 'denied') {
          try {
            await api.inspect(db.id)
            setPhase('refused')
            return
          } catch (again) {
            if (onLost) onLost(again)
          }
        }
        setError(err)
        setPhase('error')
      }
    }

    const ArtifactCard = sdk.ui.ArtifactCard
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={start} data-testid="tbl-export-dialog">
          <p data-testid="tbl-export-scope">{filtered ? t('export.scope.filtered') : t('export.scope.all')}</p>
          <p className="tbl-muted">{t('export.intro')}</p>
          {phase !== 'working' && phase !== 'done' ? (
            <label className="tbl-check">
              <input type="checkbox" checked={rowId} onChange={(e) => setRowId(e.target.checked)} />
              <span>{t('export.rowId')}</span>
            </label>
          ) : null}
          {phase === 'working' ? <Loading label={t('export.working')} /> : null}
          {phase === 'too-large' ? <Banner tone="warn" testid="tbl-export-toolarge">{t('export.tooLarge')}</Banner> : null}
          {phase === 'busy' ? <Banner tone="warn" testid="tbl-export-busy">{t('export.busy')}</Banner> : null}
          {phase === 'refused' ? <Banner tone="warn" testid="tbl-export-refused">{t('export.refused')}</Banner> : null}
          {phase === 'none' ? <Banner tone="error" testid="tbl-export-none">{t('export.none')}</Banner> : null}
          {phase === 'error' ? <Problem error={error} onRetry={start} /> : null}
          {phase === 'done' && ArtifactCard ? <div data-testid="tbl-export-file"><ArtifactCard artifactId={file} /></div> : null}
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.close')}</Button>
            {phase === 'done'
              ? <Button onClick={() => { setPhase('idle'); setFile('') }}>{t('export.again')}</Button>
              : <Button type="submit" variant="primary" busy={phase === 'working'}>{t('export.start')}</Button>}
          </div>
        </form>
      </Root>
    )
  }

  return { ExportDialog }
}
