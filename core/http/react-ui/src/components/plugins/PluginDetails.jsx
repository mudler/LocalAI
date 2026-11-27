/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, LoadError, SidePane, SkeletonRows, Tag, useLoad } from '../ui'
import { formatBytes } from '../../utils/format'
import { CodeFile, ComponentList, ContributionTags, Sha, SkippedList } from './PluginBits'

// The details of one install beside the list: its commit and digest, who
// installed it, what it adds (the model tier of each agent), the components
// left out with their reason, and its code files as text.
export default function PluginDetails({ install, api, onClose, onSettings }) {
  const { t } = useTranslation('addons')
  const detail = useLoad((signal) => api.detail(install.id, signal), [install.id])
  const loadFile = useCallback((path, signal) => api.installFile(install.id, path, signal), [api, install.id])
  const d = detail.data
  const options = d?.model?.userConfig || []
  return (
    <SidePane
      open
      wide
      title={install.name || install.plugin}
      meta={<><span className="pl-mono">{install.plugin}</span>{install.version && <Tag mono>{install.version}</Tag>}</>}
      onClose={onClose}
      testId="plugin-details"
      footer={options.length > 0 ? (
        <Button size="sm" icon="sliders" onClick={() => onSettings(install)} data-testid="plugin-details-settings">{t('plugins.installed.settings')}</Button>
      ) : null}
    >
      {detail.status === 'loading' && <SkeletonRows count={3} />}
      {detail.status === 'error' && <LoadError title={t('plugins.details.loadFailed')} error={detail.error} onRetry={detail.retry} />}
      {d && (
        <div className="pl-review pl-details">
          {install.description && <p className="pl-text">{install.description}</p>}
          <ContributionTags model={d.model} hosts={d.hosts} runsCode={d.runsCode} />
          <dl className="dk-kv pl-kv">
            <dt>{t('plugins.facts.scope')}</dt><dd>{t(`plugins.scope.${d.scope === 'instance' ? 'instance' : 'user'}`)}</dd>
            <dt>{t('plugins.facts.commit')}</dt><dd><Sha sha={d.sha} /></dd>
            <dt>{t('plugins.facts.digest')}</dt><dd className="pl-mono pl-digest" data-testid="plugin-digest">{d.digest}</dd>
            <dt>{t('plugins.facts.size')}</dt><dd>{t('plugins.facts.sizeValue', { size: formatBytes(d.sizeBytes || 0), count: (d.files || []).length })}</dd>
            {d.installedBy && (<><dt>{t('plugins.facts.installedBy')}</dt><dd>{d.installedBy}</dd></>)}
            {d.approvedBy && (<><dt>{t('plugins.facts.approvedBy')}</dt><dd>{d.approvedBy}</dd></>)}
            {d.updateSha && (<><dt>{t('plugins.facts.updateTo')}</dt><dd><Sha sha={d.updateSha} /></dd></>)}
          </dl>
          <section className="pl-section" aria-labelledby="pl-d-adds">
            <h3 className="pl-section__title" id="pl-d-adds">{t('plugins.review.adds')}</h3>
            <ComponentList model={d.model} />
          </section>
          {(d.skipped || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-d-skipped">
              <h3 className="pl-section__title" id="pl-d-skipped">{t('plugins.review.skipped')}</h3>
              <SkippedList skipped={d.skipped} />
            </section>
          )}
          {(d.codeFiles || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-d-code">
              <h3 className="pl-section__title" id="pl-d-code">{t('plugins.review.code')}</h3>
              <span className="ui-tags pl-tags">{(d.codeReasons || []).map((r) => <Tag key={r} mono>{t(`plugins.reasons.${r}`, { defaultValue: r })}</Tag>)}</span>
              {d.codeFiles.map((f) => <CodeFile key={f} path={f} load={loadFile} />)}
            </section>
          )}
        </div>
      )}
    </SidePane>
  )
}
