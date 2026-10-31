/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout, SidePane, Switch } from '../ui'
import { filterTools, mcpAddress, mcpTarget, sentence, testedWhen as when } from '../../utils/chatMCP'
import { StateChip, ToolList } from './ConnectorBits'

// The details of one connector in the shared side pane (docked beside the list
// from 1280 px, over the page below, a bottom sheet on a phone): its switch,
// the last test with the reason when it failed, the tools it offers, how it
// connects and what is saved for it. Remove is at the foot, apart from the
// actions that keep it.
export default function ConnectorDetails({ c, state, check, testing, busy, onClose, onToggle, onTest, onEdit, onReauth, onRemove }) {
  const { t, i18n } = useTranslation('chats')
  const [query, setQuery] = useState('')
  const stdio = c.transport === 'stdio'
  const tested = when(check?.at, i18n.language)
  const added = c.created ? when(Date.parse(c.created), i18n.language) : null
  const tools = check?.ok ? check.tools : null
  const shown = tools ? filterTools(tools, query) : []
  const hiddenQuery = !stdio && /\?/.test(c.url || '')
  const switchId = `mcp-use-${c.id}`

  return (
    <SidePane
      open
      wide
      title={c.name}
      onClose={onClose}
      testId="mcp-details"
      className="mcp-details"
      meta={<><StateChip state={state} /><span className="mcp-details__host" title={mcpTarget(c)}>{mcpTarget(c)}</span></>}
      footer={(
        <div className="mcp-details__foot">
          <Button size="sm" variant="danger-ghost" icon="trash" onClick={() => onRemove(c)} data-testid="mcp-details-remove">{t('mcpPage.actions.remove')}</Button>
          <span className="mcp-details__grow" />
          <Button size="sm" variant="ghost" icon="pencil" onClick={() => onEdit(c)}>{t('mcpPage.actions.edit')}</Button>
          <Button size="sm" variant="primary" icon="refresh" busy={testing} onClick={() => onTest(c)} data-testid="mcp-details-test">
            {testing ? t('mcpPage.actions.testing') : t('mcpPage.actions.test')}
          </Button>
        </div>
      )}
    >
      <div className="mcp-details__body">
        <div className="mcp-use">
          <div className="mcp-use__text">
            <span className="mcp-use__label" id={`${switchId}-label`}>{t('mcpPage.details.use')}</span>
            <span className="mcp-use__help" id={`${switchId}-help`}>{t('mcpPage.details.useHelp')}</span>
          </div>
          <Switch checked={c.enabled} disabled={busy} onChange={(on) => onToggle(c, on)} labelledBy={`${switchId}-label`} describedBy={`${switchId}-help`} testId="mcp-details-switch" />
        </div>

        <section className="mcp-section" aria-labelledby="mcp-sec-check">
          <h3 className="mcp-section__title" id="mcp-sec-check">{t('mcpPage.details.lastCheck')}</h3>
          {!check && <p className="mcp-section__text">{t('mcpPage.details.never')}</p>}
          {check?.ok && (
            <p className="mcp-section__text mcp-check-ok" title={tested.exact}>
              <Icon name="check-circle" aria-hidden="true" /> {t('mcpPage.details.okAt', { when: tested.label })}
            </p>
          )}
          {check && !check.ok && (
            <Callout
              tone={state === 'auth' ? 'warn' : 'error'}
              icon={state === 'auth' ? 'key' : undefined}
              title={state === 'auth' ? t('mcpPage.details.authTitle') : t('mcpPage.details.errorTitle')}
              testId="mcp-details-error"
              actions={state === 'auth' && !stdio ? <Button size="sm" onClick={() => onReauth(c)}>{t('mcpPage.actions.reauth')}</Button> : null}
            >
              <span className="mcp-reason">{sentence(check.error)}</span>{' '}
              <span className="mcp-quiet" title={tested.exact}>{t('mcpPage.details.testedAt', { when: tested.label })}</span>
            </Callout>
          )}
        </section>

        <section className="mcp-section" aria-labelledby="mcp-sec-tools">
          <h3 className="mcp-section__title" id="mcp-sec-tools">
            {t('mcpPage.details.tools')}
            {tools && <span className="ad-tab__count">{tools.length}</span>}
          </h3>
          {!tools && <p className="mcp-section__text">{t('mcpPage.details.toolsUnknown')}</p>}
          {tools && tools.length === 0 && <p className="mcp-section__text">{t('mcpPage.details.toolsNone')}</p>}
          {tools && tools.length > 5 && (
            <span className="dk-input-icon mcp-tools-search">
              <Icon name="search" className="dk-icon" />
              <input type="search" className="dk-input" value={query} onChange={(e) => setQuery(e.target.value)}
                placeholder={t('mcpPage.details.toolsSearch')} aria-label={t('mcpPage.details.toolsSearch')} data-testid="mcp-tools-search" />
            </span>
          )}
          {tools && tools.length > 0 && shown.length === 0 && (
            <p className="mcp-section__text">{t('mcpPage.details.noToolMatch', { query: query.trim() })}</p>
          )}
          {shown.length > 0 && <ToolList tools={shown} />}
        </section>

        <section className="mcp-section" aria-labelledby="mcp-sec-conn">
          <h3 className="mcp-section__title" id="mcp-sec-conn">{t('mcpPage.details.connection')}</h3>
          <dl className="mcp-kv">
            <dt>{t('mcpPage.details.kind')}</dt>
            <dd>{stdio ? t('mcpPage.sheet.local') : t('mcpPage.sheet.remote')}</dd>
            <dt>{stdio ? t('mcpPage.details.command') : t('mcpPage.details.address')}</dt>
            <dd className="mcp-mono" data-testid="mcp-details-address">{mcpAddress(c)}</dd>
            {added?.label && (<><dt>{t('mcpPage.details.added')}</dt><dd title={added.exact}>{added.label}</dd></>)}
          </dl>
          {hiddenQuery && <p className="dk-hint">{t('mcpPage.details.queryHidden')}</p>}
        </section>

        <section className="mcp-section" aria-labelledby="mcp-sec-auth">
          <h3 className="mcp-section__title" id="mcp-sec-auth">{t('mcpPage.details.signIn')}</h3>
          <dl className="mcp-kv">
            {stdio ? (
              <><dt>{t('mcpPage.details.env')}</dt><dd>{c.has_env ? t('mcpPage.details.saved') : t('mcpPage.details.none')}</dd></>
            ) : (
              <>
                <dt>{t('mcpPage.details.token')}</dt><dd>{c.has_token ? t('mcpPage.details.saved') : t('mcpPage.details.none')}</dd>
                <dt>{t('mcpPage.details.headers')}</dt><dd>{c.has_headers ? t('mcpPage.details.saved') : t('mcpPage.details.none')}</dd>
              </>
            )}
          </dl>
          <p className="mcp-note"><Icon name="lock" aria-hidden="true" />{t('mcpPage.details.encrypted')}</p>
        </section>
      </div>
    </SidePane>
  )
}
