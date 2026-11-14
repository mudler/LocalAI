/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import SideSheet from '../SideSheet'
import { Callout, Switch } from '../ui'
import { sourceText } from '../../utils/modelSearch'

// The panel that turns online search sources on and off. One row for each
// installed source, built in or from an add-on. Every source is on until the
// person turns it off. The choice is theirs and takes effect at once.
//
//   sources   every installed source: { id, title, kind, p2p, i18n }
//   enabled   the IDs that are on
//   missing   IDs with no installed add-on, listed apart with a link to the
//             add-ons. The server sends none today; an older one may.
export default function SourcesPanel({ sources, enabled, missing, busy, error, onToggle, onClose, lang, t }) {
  return (
    <SideSheet title={t('remote.sourcesTitle')} onClose={onClose} closeLabel={t('remote.sourcesClose')} labelId="model-sources-title" testId="model-sources-panel">
      <p className="source-panel__help">{t('remote.sourcesHelp')}</p>
      <ul className="source-panel__list">
        {sources.map(s => {
          const title = sourceText(s, lang)
          return (
            <li key={s.id} className="source-panel__row" data-testid={`source-row-${s.id}`}>
              <span className="source-panel__name">
                {title}
                {s.p2p && <span className="dk-chip__tag">{t('remote.p2p')}</span>}
                <span className="source-panel__kind">{s.kind === 'builtin' ? t('remote.sourceBuiltin') : t('remote.sourceAddon')}</span>
              </span>
              <Switch
                checked={enabled.includes(s.id)}
                disabled={busy}
                label={title}
                testId={`source-switch-${s.id}`}
                onChange={(on) => onToggle(s.id, on)}
              />
            </li>
          )
        })}
        {missing.map(id => (
          <li key={id} className="source-panel__row" data-testid={`source-missing-${id}`}>
            <span className="source-panel__name">
              {id}
              <span className="source-panel__kind">{t('remote.sourceNotInstalled')}</span>
            </span>
          </li>
        ))}
      </ul>
      {missing.length > 0 && (
        <Callout tone="info" testId="source-missing-hint">
          {t('remote.missingHint', { sources: missing.join(', ') })} <Link to="/addons">{t('remote.missingInstall')}</Link>
        </Callout>
      )}
      {error && <Callout tone="error" testId="source-error">{t('remote.sourcesError')}</Callout>}
    </SideSheet>
  )
}
