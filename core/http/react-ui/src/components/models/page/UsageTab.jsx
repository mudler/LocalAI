// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useCleanupFacts } from '../../../hooks/useCleanupFacts'
import { formatBytes } from '../../../utils/format'
import { modelPath } from '../../../utils/modelWalk'
import { diskEntry, filesOf } from '../../../utils/modelStorage'
// eslint-disable-next-line no-unused-vars
import NodeDistributionChip from '../../NodeDistributionChip'
import Icon from '../../Icon'

const MISSING = ['requests', 'ttft', 'loads', 'changes']

// The files this model uses on disk, with their size and who else uses them.
// A file another installed model uses stays on disk when this one is removed,
// and a name the config gives that is not on disk is flagged. Shown to admins,
// who can read the report; for anyone else the section says why it is absent.
// eslint-disable-next-line no-unused-vars
function FilesOnDisk({ view }) {
  const { t, id, storage } = view
  const entry = diskEntry(storage.index, id)
  const rows = filesOf(storage.index, id)
  return (
    <section aria-labelledby="modelpage-files-h" data-testid="model-page-files">
      <div className="modelpage-section-head">
        <h2 className="modelpage-section-title" id="modelpage-files-h">{t('page.files.title')}</h2>
        {entry && <span className="modelpage-section-note">{t('page.files.total', { size: formatBytes(entry.size), count: entry.files.length })}</span>}
      </div>
      {storage.status === 'loading' ? (
        <span className="dk-skeleton dk-skeleton--block modelpage-variants__loading" role="status" aria-label={t('page.files.title')} />
      ) : !entry ? (
        <p className="dk-hint" data-testid="files-on-disk-none">
          {storage.status === 'ready' ? t('storage.empty') : t('page.files.unavailable')}
        </p>
      ) : (
        <>
          {entry.missing.length > 0 && (
            <div className="modelpage-banner" data-tone="warn" role="status" data-testid="files-missing-banner">
              <Icon name="alert-circle" />
              <span>{t('page.files.missing')}</span>
            </div>
          )}
          <div className="dk-table-wrap modelpage-table" role="region" aria-label={t('page.files.title')} tabIndex={0}>
            <table className="dk-table dk-table--compact">
              <caption className="dk-sr-only">{t('page.files.caption', { model: id })}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('storage.columns.file')}</th>
                  <th scope="col" className="dk-num">{t('storage.columns.size')}</th>
                  <th scope="col">{t('storage.columns.status')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(f => (
                  <tr key={f.path} data-testid="disk-file-row" data-missing={f.missing ? 'true' : 'false'}>
                    <td className="dk-table-id dk-mono modelpage-files__path">{f.path}</td>
                    <td className="dk-num dk-mono">{f.missing ? '\u2014' : formatBytes(f.size)}</td>
                    <td>
                      {f.missing && <span className="dk-badge dk-badge--warn">{t('storage.status.missing')}</span>}
                      {!f.missing && f.others.length > 0 && (
                        <span className="modelpage-files__shared">
                          {t('page.files.sharedWith')}{' '}
                          {f.others.map((other, i) => (
                            <span key={other}>{i > 0 && ', '}<Link className="dk-link dk-mono" to={modelPath(other)}>{other}</Link></span>
                          ))}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {entry.shared > 0 && <p className="dk-hint modelpage-files__note">{t('page.files.sharedNote')}</p>}
        </>
      )}
    </section>
  )
}

// Usage and history. The API keeps no per-model request count, no time to first
// token and no record of loads or configuration changes, so there is nothing to
// chart and the tab says so, naming what will appear and what is missing. What
// the server does know about the model today (its state, who uses it) is listed
// as facts, so the tab is not empty of the truth.
export default function UsageTab({ view }) {
  const { t, id, profile, running, storage } = view
  const disk = diskEntry(storage?.index, id)
  const facts = useCleanupFacts(true)
  const refs = facts.references.get(id) || []
  const kinds = ['agent', 'task', 'chain', 'alias']
    .map(kind => ({ kind, count: refs.filter(ref => ref.kind === kind).length }))
    .filter(item => item.count > 0)
  const nodes = Array.isArray(profile?.loaded_on) ? profile.loaded_on : []
  return (
    <div className="modelpage-usage" data-testid="model-page-usage">
      <section aria-labelledby="modelpage-usage-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-usage-h">{t('page.usage.title')}</h2>
        </div>
        <div className="dk-empty modelpage-usage__empty" data-testid="usage-empty">
          <div className="dk-empty-icon"><Icon name="chart-bar" /></div>
          <h3 className="dk-empty-title">{t('page.usage.emptyTitle')}</h3>
          <p className="dk-empty-text">{t('page.usage.emptyText')}</p>
        </div>
        <ul className="modelpage-usage__missing" aria-label={t('page.usage.missingLabel')} data-testid="usage-missing">
          {MISSING.map(key => (
            <li key={key}>
              <span>
                <strong>{t(`page.usage.missing.${key}.name`)}</strong>
                <span className="dk-hint">{t(`page.usage.missing.${key}.why`)}</span>
              </span>
              <span className="dk-badge">{t('page.usage.notRecorded')}</span>
            </li>
          ))}
        </ul>
      </section>

      <section aria-labelledby="modelpage-known-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-known-h">{t('page.usage.known')}</h2>
        </div>
        <dl className="dk-kv modelpage-facts" data-testid="usage-known">
          <dt>{t('lifecycle.detail.state')}</dt>
          <dd>{profile.disabled ? t('lifecycle.states.disabled') : running ? t('lifecycle.states.running') : t('lifecycle.states.idle')}</dd>
          {nodes.length > 0 && (
            <>
              <dt>{t('lifecycle.detail.distributed')}</dt>
              <dd><NodeDistributionChip nodes={nodes} context="models" compactThreshold={20} /></dd>
            </>
          )}
          {disk && (
            <>
              <dt>{t('lifecycle.detail.size')}</dt>
              <dd data-testid="usage-size">
                {formatBytes(disk.size)}
                {disk.shared > 0 && <span className="cell-muted"> {'\u00b7'} {t('lifecycle.detail.sharedBytes', { size: formatBytes(disk.shared) })}</span>}
              </dd>
            </>
          )}
          <dt>{t('page.usedBy.title')}</dt>
          <dd data-testid="usage-usedby">
            {!facts.loaded
              ? t('page.usedBy.loading')
              : kinds.length === 0
                ? (facts.verified ? t('page.usedBy.none') : t('page.usedBy.unknown'))
                : kinds.map(item => t(`page.usage.count.${item.kind}`, { count: item.count })).join(', ')}
          </dd>
        </dl>
      </section>

      <FilesOnDisk view={view} />
    </div>
  )
}
