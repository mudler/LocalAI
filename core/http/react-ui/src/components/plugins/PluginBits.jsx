/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { LoadingSpinner, Tag } from '../ui'
import { contributions, hookHandlerText, mcpServerText, shortSha, skippedKey } from './pluginState'

// The parts the Plugins tab, its review dialogs and its details pane share.

// What a plugin adds, as tags: "4 skills", "Connects to host", plus the
// "Runs code" and "Needs Sandbox" marks, or "No code". runsCode null (not
// known yet) draws no mark: the browse card says "Not checked yet".
export function ContributionTags({ model, hosts, runsCode, testId = 'plugin-adds' }) {
  const { t } = useTranslation('addons')
  const chips = contributions(model, { hosts })
  if (chips.length === 0 && typeof runsCode !== 'boolean') return null
  return (
    <span className="ui-tags pl-tags" data-testid={testId}>
      {chips.map((c) => (
        <Tag key={c.key + (c.host || '')} icon={ICON[c.key]} title={c.host ? t('plugins.adds.connectsTitle', { host: c.host }) : undefined}>
          {c.host ? t('plugins.adds.connects', { host: c.host }) : t(`plugins.adds.${c.key}`, { count: c.count })}
        </Tag>
      ))}
      {runsCode === true && <CodeMarks />}
      {runsCode === false && <Tag icon="check" testId="plugin-no-code">{t('plugins.adds.noCode')}</Tag>}
    </span>
  )
}

const ICON = { skills: 'book', commands: 'terminal', agents: 'robot', connects: 'globe', hooks: 'bolt' }

// "Runs code" and "Needs Sandbox": plugin code runs only in the Sandbox of a chat.
export function CodeMarks() {
  const { t } = useTranslation('addons')
  return (
    <>
      <Tag tone="warn" icon="code" testId="plugin-runs-code">{t('plugins.adds.runsCode')}</Tag>
      <Tag icon="shield" title={t('plugins.adds.sandboxTitle')} testId="plugin-needs-sandbox">{t('plugins.adds.sandbox')}</Tag>
    </>
  )
}

// A short commit with the full one in its tooltip.
export function Sha({ sha, testId }) {
  if (!sha) return null
  return <span className="pl-mono" title={sha} data-testid={testId}>{shortSha(sha)}</span>
}

// The components of a plugin, one list per kind. The commands show as the
// person types them, with the hint of their arguments.
export function ComponentList({ model, testId = 'plugin-components' }) {
  const { t } = useTranslation('addons')
  if (!model) return null
  const name = model.name || ''
  const groups = [
    { key: 'skills', icon: 'book', rows: (model.skills || []).map((s) => ({ id: s.name, label: `${name}:${s.name}`, desc: s.description, hint: s.userInvocable && s.argumentHint ? s.argumentHint : '' })) },
    { key: 'commands', icon: 'terminal', rows: (model.commands || []).map((c) => ({ id: c.name, label: `/${name}:${c.name}`, desc: c.description, hint: c.argumentHint })) },
    { key: 'agents', icon: 'robot', rows: (model.agents || []).map((a) => ({ id: a.name, label: `${name}:${a.name}`, desc: a.description, hint: a.model ? t('plugins.components.tier', { tier: a.model }) : '' })) },
    { key: 'mcpServers', icon: 'plug', rows: (model.mcpServers || []).map((s) => ({ id: s.name, label: s.name, desc: mcpServerText(s), hint: s.type })) },
    { key: 'hooks', icon: 'bolt', rows: (model.hooks || []).map((h, i) => ({ id: `${h.event}-${i}`, label: h.event, desc: (h.handlers || []).map(hookHandlerText).filter(Boolean).join(' · '), hint: h.matcher || '' })) },
  ].filter((g) => g.rows.length > 0)
  if (groups.length === 0) return <p className="pl-quiet">{t('plugins.components.none')}</p>
  return (
    <div className="pl-components" data-testid={testId}>
      {groups.map((g) => (
        <section key={g.key} className="pl-components__group" aria-label={t(`plugins.components.${g.key}`)}>
          <h4 className="pl-subhead"><Icon name={g.icon} aria-hidden="true" /> {t(`plugins.components.${g.key}`)} <span className="pl-count">{g.rows.length}</span></h4>
          <ul className="pl-rows">
            {g.rows.map((r) => (
              <li key={r.id} className="pl-row">
                <span className="pl-row__name pl-mono">{r.label}</span>
                {r.hint && <span className="pl-row__hint pl-mono">{r.hint}</span>}
                {r.desc && <span className="pl-row__desc">{r.desc}</span>}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}

// The components the parser left out, with their reason.
export function SkippedList({ skipped }) {
  const { t } = useTranslation('addons')
  const list = Array.isArray(skipped) ? skipped : []
  if (list.length === 0) return null
  return (
    <ul className="pl-rows" data-testid="plugin-skipped">
      {list.map((s, i) => {
        const code = s.reasonCode || s.reason || ''
        const key = skippedKey(code)
        return (
          <li key={`${s.component}-${s.name}-${i}`} className="pl-row">
            <span className="pl-row__name">{[s.component, s.name].filter(Boolean).join(': ') || code}</span>
            <span className="pl-row__desc">{key ? t(key) : code}</span>
          </li>
        )
      })}
    </ul>
  )
}

// One file for review: fetched as plain text when it opens and drawn as
// text. load(path, signal) returns the text.
export function CodeFile({ path, load, open: openInitially = false, testId = 'plugin-code-file' }) {
  const { t } = useTranslation('addons')
  const [open, setOpen] = useState(openInitially)
  const [state, setState] = useState({ status: 'idle', text: '', error: '' })
  useEffect(() => {
    if (!open || state.status !== 'idle') return undefined
    const ctl = new AbortController()
    setState({ status: 'loading', text: '', error: '' })
    load(path, ctl.signal)
      .then((text) => setState({ status: 'ready', text: String(text), error: '' }))
      .catch((e) => { if (!ctl.signal.aborted) setState({ status: 'error', text: '', error: e?.message || '' }) })
    return () => ctl.abort()
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <details className="ad-disclosure pl-file" open={open} onToggle={(e) => setOpen(e.currentTarget.open)} data-testid={testId} data-path={path}>
      <summary><Icon name="chevron-right" aria-hidden="true" className="ad-disclosure__icon" /> <span className="pl-mono">{path}</span></summary>
      {state.status === 'loading' && <LoadingSpinner size="sm" />}
      {state.status === 'error' && <p className="pl-error" role="alert">{t('plugins.review.fileFailed', { error: state.error })}</p>}
      {state.status === 'ready' && <pre className="pl-code" data-testid={`${testId}-text`}>{state.text}</pre>}
    </details>
  )
}

// A unified diff drawn as text, one line each, marked added or removed.
export function DiffView({ lines }) {
  return (
    <pre className="pl-code pl-diff" data-testid="plugin-diff">
      {lines.map((l, i) => <span key={i} className="pl-diff__line" data-kind={l.kind}>{l.text}{'\n'}</span>)}
    </pre>
  )
}
