import { useEffect, useState } from 'react'
import { modelsApi } from '../../../utils/api'
import { useGalleryEntry } from '../../../hooks/useModelPage'
import { fitFor, gbNumber } from '../../../utils/modelLedger'
import { contextLabel } from '../../../utils/placement'
import { formatBytes } from '../../../utils/format'
import Icon from '../../Icon'

function variantFeatureLabel(feature, t) {
  return t(`variants.features.${feature}`, { defaultValue: feature.toUpperCase() })
}

// Estimates for every build at the context on screen, so each row can say how
// it fits without opening it. One request per build, started together: an
// entry offers a handful.
function useBuildFits(variants, contextSize) {
  const [bytes, setBytes] = useState({})
  const key = variants.map(v => v.model).join('\n')
  useEffect(() => {
    let cancelled = false
    variants.forEach(v => {
      modelsApi.estimate(v.model, [contextSize])
        .then(data => {
          const value = data?.estimates?.[String(contextSize)]?.vramBytes
          if (!cancelled && value > 0) setBytes(prev => ({ ...prev, [`${v.model}|${contextSize}`]: value }))
        })
        .catch(() => { /* a build with no estimate falls back to the server's own fits flag */ })
    })
    return () => { cancelled = true }
  // The list is read by its names; a new array with the same names is the same list.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, contextSize])
  return bytes
}

// Variants and files: the builds of one model with their size and fit, one of
// them chosen for the Install button, and the files that build downloads.
export default function VariantsTab({ view }) {
  const {
    t, id, entry, variants, variantsState, build, autoBuild, pickBuild, install, installing, installedIds,
    budget, ramAvailable, contextSize, progress,
  } = view
  const activeBuild = build || autoBuild || id
  const bytesByBuild = useBuildFits(variants, contextSize)
  const buildEntry = useGalleryEntry(activeBuild !== id ? activeBuild : null)
  const filesEntry = activeBuild === id ? entry : buildEntry.entry
  const files = filesEntry?.additionalFiles || filesEntry?.files || []
  const activeVariant = variants.find(v => v.model === activeBuild)
  const isInstalled = installedIds.has(activeBuild)

  const fitOf = (variant) => {
    const bytes = bytesByBuild[`${variant.model}|${contextSize}`]
    const fit = fitFor(bytes, budget, ramAvailable)
    if (fit) {
      return {
        state: fit.state,
        text: fit.state === 'fits'
          ? (budget.hasGpu ? t('page.variants.fitsGpu') : t('page.variants.fitsMemory'))
          : fit.state === 'spill'
            ? t('page.variants.spill', { amount: gbNumber(fit.amount) })
            : t('page.variants.over', { amount: gbNumber(fit.amount) }),
      }
    }
    if (variant.fits === true) return { state: 'fits', text: t('page.variants.fitsServer') }
    if (variant.fits === false) return { state: 'over', text: t('variants.doesNotFit') }
    return { state: 'none', text: t('ledger.fit.noEstimate') }
  }

  return (
    <div className="modelpage-variants" data-testid="model-page-variants">
      <section aria-labelledby="modelpage-variants-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-variants-h">{t('variants.title')}</h2>
          <span className="modelpage-section-note">{t('page.variants.fitAt', { context: contextLabel(contextSize) })}</span>
        </div>
        {variantsState.status === 'loading' && (
          <span className="dk-skeleton dk-skeleton--block modelpage-variants__loading" role="status" aria-label={t('variants.loading')} />
        )}
        {variantsState.status === 'error' && (
          <p className="dk-hint" role="status" data-testid="variants-error">{t('page.variants.error')}</p>
        )}
        {variants.length > 0 ? (
          <div className="dk-table-wrap modelpage-table" role="region" aria-label={t('variants.title')} tabIndex={0}>
            <table className="dk-table">
              <caption className="dk-sr-only">{t('page.variants.caption')}</caption>
              <thead>
                <tr>
                  <th scope="col" className="modelpage-table__radio"><span className="dk-sr-only">{t('page.variants.chosen')}</span></th>
                  <th scope="col">{t('page.variants.build')}</th>
                  <th scope="col" className="dk-hide-phone">{t('detail.backend')}</th>
                  <th scope="col" className="dk-num dk-hide-phone">{t('detail.size')}</th>
                  <th scope="col">{t('page.variants.fit')}</th>
                  <th scope="col"><span className="dk-sr-only">{t('table.actions')}</span></th>
                </tr>
              </thead>
              <tbody>
                {variants.map(v => {
                  const fit = fitOf(v)
                  const chosen = v.model === activeBuild
                  const have = installedIds.has(v.model)
                  return (
                    <tr key={v.model} data-row data-selected={chosen ? 'true' : 'false'} data-testid={`variant-row-${v.model}`} data-entity={v.model}>
                      <td className="modelpage-table__radio">
                        <input
                          className="dk-radio"
                          type="radio"
                          name="modelpage-build"
                          checked={chosen}
                          aria-label={t('page.variants.choose', { build: v.quantization || v.model })}
                          data-testid={`variant-choose-${v.model}`}
                          onChange={() => pickBuild(v.model)}
                        />
                      </td>
                      <td>
                        <span className="dk-table-name dk-mono">{v.quantization || t('variants.unknownQuantization')}</span>
                        <span className="dk-table-sub">
                          {v.model !== v.quantization ? v.model : ''}
                          <span className="modelpage-variants__size dk-mono"> · {v.memory_bytes ? formatBytes(v.memory_bytes) : t('variants.unknownSize')}</span>
                          {v.model === autoBuild && <span className="dk-badge dk-badge--ok modelpage-inline-badge"><Icon name="check-circle" /> {t('variants.autoSelected')}</span>}
                          {v.is_base && v.model !== autoBuild && <span className="dk-badge modelpage-inline-badge">{t('variants.base')}</span>}
                          {(v.features || []).map(f => (
                            <span key={f} className="dk-badge dk-badge--accent modelpage-inline-badge"><Icon name="bolt" /> {variantFeatureLabel(f, t)}</span>
                          ))}
                        </span>
                      </td>
                      <td className="dk-hide-phone dk-mono">{v.backend || t('variants.unknownBackend')}</td>
                      <td className="dk-num dk-mono dk-hide-phone">{v.memory_bytes ? formatBytes(v.memory_bytes) : t('variants.unknownSize')}</td>
                      <td>
                        <span className="modelpage-fitcell" data-fit={fit.state}>
                          <span className={`dk-dot${fit.state === 'fits' ? ' dk-dot--ok' : fit.state === 'spill' ? ' dk-dot--warn' : fit.state === 'over' ? ' dk-dot--error' : ''}`} />
                          {fit.text}
                        </span>
                      </td>
                      <td className="dk-table-actions">
                        {have ? (
                          <span className="ledger-status__done"><Icon name="check" /> {t('table.installed')}</span>
                        ) : (
                          <button
                            type="button"
                            className="dk-btn dk-btn--secondary dk-btn--sm"
                            disabled={installing}
                            aria-label={t('variants.installVariant', { variant: v.model })}
                            data-testid={`variant-install-${v.model}`}
                            onClick={() => install(v.model)}
                          >
                            <Icon name="download" /> {t('actions.install')}
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : variantsState.status !== 'loading' && variantsState.status !== 'error' && (
          <p className="dk-hint" data-testid="variants-single">{t('page.variants.single')}</p>
        )}
      </section>

      <section aria-labelledby="modelpage-files-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-files-h">{t('detail.files')}</h2>
          <span className="modelpage-section-note">
            {[activeVariant?.quantization, t('detail.fileCount', { count: files.length })].filter(Boolean).join(', ')}
          </span>
        </div>
        {buildEntry.status === 'loading' && activeBuild !== id ? (
          <span className="dk-skeleton dk-skeleton--block modelpage-variants__loading" role="status" aria-label={t('variants.detailsLoading')} />
        ) : files.length === 0 ? (
          <p className="dk-hint" data-testid="files-none">{buildEntry.status === 'missing' || buildEntry.status === 'error' ? t('variants.detailsUnavailable', { variant: activeBuild }) : t('page.variants.noFiles')}</p>
        ) : (
          <div className="dk-table-wrap modelpage-table" role="region" aria-label={t('detail.files')} tabIndex={0}>
            <table className="dk-table dk-table--compact">
              <caption className="dk-sr-only">{t('page.variants.filesCaption', { build: activeBuild })}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('detail.filename')}</th>
                  <th scope="col" className="dk-hide-phone">{t('page.variants.source')}</th>
                  <th scope="col">{t('page.variants.status')}</th>
                  <th scope="col" className="dk-hide-phone">{t('detail.sha256')}</th>
                </tr>
              </thead>
              <tbody>
                {files.map((f, i) => (
                  <tr key={`${f.filename || f.uri}-${i}`} data-testid="file-row">
                    <td className="dk-table-id dk-mono">{f.filename || '—'}</td>
                    <td className="modelpage-table__source dk-hide-phone dk-mono" title={f.uri}>{f.uri || '—'}</td>
                    <td>{installing && activeBuild === (build || autoBuild || id) && !isInstalled ? t('page.variants.downloading', { percent: progress }) : isInstalled ? t('page.variants.onDisk') : t('page.variants.notDownloaded')}</td>
                    <td className="dk-table-id dk-mono dk-hide-phone" title={f.sha256}>{f.sha256 ? `${f.sha256.slice(0, 16)}…` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
