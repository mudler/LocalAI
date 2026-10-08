import { PAGE_CONTEXTS } from '../../../hooks/useModelPage'
import { FIT_LIMIT, gbLabel } from '../../../utils/modelLedger'
import { contextLabel } from '../../../utils/placement'
import Icon from '../../Icon'
// eslint-disable-next-line no-unused-vars
import MemoryBar from '../MemoryBar'
// eslint-disable-next-line no-unused-vars
import VramChart from './VramChart'
import { fitWords, memorySegments } from './fitFacts'

// Fit and memory: will this build run here, at which context length, and how
// the memory is spent. Every figure is the server's estimate for the build and
// the context on screen, measured against the memory the Models list uses.
export default function FitTab({ view }) {
  const { t, estimateState, fit, budget, contextSize, setContextSize, variants, build, autoBuild, pickBuild, id, installed } = view
  const words = fitWords(view)
  const points = PAGE_CONTEXTS
    .map(ctx => ({ ctx, bytes: estimateState.data?.estimates?.[String(ctx)]?.vramBytes || 0 }))
    .filter(point => point.bytes > 0)
  const grows = points.length > 1 && Math.max(...points.map(p => p.bytes)) > Math.min(...points.map(p => p.bytes)) * 1.01
  const activeBuild = build || autoBuild || id
  const pool = budget.hasGpu ? t('page.fit.poolGpu', { memory: gbLabel(fit?.total || 0) }) : t('page.fit.poolRam', { memory: gbLabel(fit?.total || 0) })
  const where = budget.scope === 'cluster' && budget.nodeName ? t('ledger.summary.onNode', { node: budget.nodeName }) : ''

  return (
    <div className="modelpage-fit" data-testid="model-page-fit">
      <div className="modelpage-section-head">
        <h2 className="modelpage-section-title">{t('page.tabs.fit')}{where}</h2>
        {variants.length > 1 && (
          <div className="dk-segmented modelpage-builds-seg" role="group" aria-label={t('page.fit.build')} data-testid="fit-builds">
            {variants.map(v => (
              <button
                key={v.model}
                type="button"
                className="dk-seg dk-mono"
                aria-pressed={v.model === activeBuild}
                data-testid={`fit-build-${v.model}`}
                onClick={() => pickBuild(v.model)}
              >
                {v.quantization || v.model}
              </button>
            ))}
          </div>
        )}
      </div>

      {estimateState.status === 'loading' && (
        <div className="modelpage-fit__loading" role="status" aria-label={t('page.fit.loading')} data-testid="fit-loading">
          <span className="dk-skeleton dk-skeleton--line modelpage-fit__skeleton" />
          <span className="dk-skeleton dk-skeleton--block modelpage-fit__skeleton-block" />
        </div>
      )}

      {estimateState.status === 'unavailable' && (
        <div className="dk-empty modelpage-fit__none" data-testid="fit-unavailable">
          <div className="dk-empty-icon"><Icon name="info" /></div>
          <h3 className="dk-empty-title">{t('page.fit.unavailableTitle')}</h3>
          <p className="dk-empty-text">{installed ? t('page.fit.unavailableInstalled') : t('page.fit.unavailableText')}</p>
        </div>
      )}

      {estimateState.status === 'ready' && (
        <>
          {words ? (
            <div className="modelpage-banner" data-tone={words.tone} data-testid="fit-banner" role="status">
              <Icon name={words.tone === 'ok' ? 'check-circle' : words.tone === 'warn' ? 'alert-circle' : 'close-circle'} />
              <span><strong>{words.title}.</strong> {words.text}</span>
            </div>
          ) : (
            <p className="dk-hint" data-testid="fit-no-basis">{t('page.fit.noMachine')}</p>
          )}

          <div className="modelpage-context">
            <span className="dk-label" id="modelpage-context-label">{t('page.fit.context')}</span>
            <div className="dk-segmented" role="group" aria-labelledby="modelpage-context-label">
              {PAGE_CONTEXTS.map(ctx => (
                <button
                  key={ctx}
                  type="button"
                  className="dk-seg"
                  aria-pressed={ctx === contextSize}
                  data-testid={`fit-context-${ctx}`}
                  onClick={() => setContextSize(ctx)}
                >
                  {contextLabel(ctx)}
                </button>
              ))}
            </div>
          </div>

          {fit && (
            <div className="modelpage-pool" data-testid="fit-pool">
              <div className="modelpage-glance__head">
                <span className="modelpage-glance__pool">{pool}</span>
                <span className={`modelpage-glance__free${fit.limit - fit.need < 0 ? ' modelpage-glance__free--over' : ''}`}>
                  {fit.limit - fit.need < 0
                    ? t('placement.bar.over', { amount: gbLabel(fit.need - fit.limit) })
                    : t('placement.bar.freeAfter', { amount: gbLabel(fit.limit - fit.need) })}
                </span>
              </div>
              <MemoryBar
                capacity={fit.limit}
                segments={memorySegments(estimateState.data, contextSize, fit.need, t)}
                ariaLabel={t('page.fit.barAria', { pool, need: gbLabel(fit.need), limit: gbLabel(fit.limit) })}
                testId="fit-bar"
              />
              <p className="dk-hint">{t('page.fit.limitNote', { percent: Math.round(FIT_LIMIT * 100) })}</p>
            </div>
          )}

          {fit && grows && (
            <VramChart
              points={points}
              limit={fit.limit}
              selected={contextSize}
              onPick={setContextSize}
              title={t(budget.hasGpu ? 'page.fit.chartTitle' : 'page.fit.chartTitleRam', { build: activeBuild })}
              poolLabel={t('page.fit.limitLabel', { memory: gbLabel(fit.limit), pool: budget.hasGpu ? t('page.fit.gpuMemory') : t('page.fit.systemMemory') })}
              t={t}
            />
          )}
          {fit && !grows && points.length > 1 && (
            <p className="dk-hint" data-testid="fit-flat">{t('page.fit.flat')}</p>
          )}
          <p className="dk-hint">{t('page.fit.footnote')}</p>
        </>
      )}
    </div>
  )
}
