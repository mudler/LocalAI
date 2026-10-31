/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { formatWhen } from '../addons/updateState'
import { Link } from 'react-router-dom'
import { Callout, LoadError, SkeletonRows, Switch } from '../ui'
import { chatBuiltinToolsApi } from '../../utils/api'
import { FIELD_BY_KEY, getValue, setValue } from '../../utils/settingsSchema'
import { SANDBOX_INSTALL_LINK } from '../../utils/chatCommands'

// Settings > Tools: the built-in chat tools (bash, web_search, web_fetch), each
// of which an add-on may replace, from GET /api/chats/admin/tools. Each row has its switch, says who serves the tool
// now, and lets an administrator choose the add-on when several replace it.
// Below them, the built-in skills that installed add-ons replace (xlsx, say),
// chosen the same way and in the same chats_tool_providers map.
// The changes are drafts of the page, applied with the other settings.

const MODE = FIELD_BY_KEY.chats_host_exec
const PINS = FIELD_BY_KEY.chats_tool_providers

function readPins(settings) {
  try {
    const text = String(getValue(PINS, settings) || '').trim()
    const v = text ? JSON.parse(text) : {}
    return v && typeof v === 'object' && !Array.isArray(v) ? v : {}
  } catch {
    return {}
  }
}

function writePins(settings, pins) {
  const clean = Object.fromEntries(Object.entries(pins).filter(([, v]) => v))
  return setValue(PINS, settings, Object.keys(clean).length ? JSON.stringify(clean, null, 2) : '')
}

// The bash row: on or off, and when on whether each command waits for an
// administrator's yes.
function BashControls({ tool, settings, onChange }) {
  const { t } = useTranslation('admin')
  const labelId = useId()
  const draft = String(getValue(MODE, settings) || '')
  // An empty draft is the server's default, which the list reports.
  const mode = draft || tool.mode || 'off'
  const set = (v) => onChange(setValue(MODE, settings, v))
  return (
    <div className="st-tools__controls">
      <div className="st-tools__switch">
        <Switch checked={mode !== 'off'} labelledBy={labelId} onChange={(on) => set(on ? 'ask' : 'off')} testId="settings-tools-bash-switch" />
        <span id={labelId}>{t('settingsTools.bash.switch')}</span>
      </div>
      {mode !== 'off' && (
        <fieldset className="st-tools__choice" data-testid="settings-tools-bash-mode">
          <legend className="dk-sr-only">{t('settingsTools.bash.modeLegend')}</legend>
          {['ask', 'on'].map(m => (
            <label key={m} className="st-tools__radio">
              <input type="radio" name="chats_host_exec" value={m} checked={mode === m} onChange={() => set(m)} />
              <span><b>{t(`settingsTools.bash.${m}`)}</b> {t(`settingsTools.bash.${m}Hint`)}</span>
            </label>
          ))}
        </fieldset>
      )}
      {!draft && <p className="st-row__desc">{t('settingsTools.bash.default', { mode: t(`settingsTools.modes.${tool.mode || 'off'}`) })}</p>}
      <p className="st-row__desc" data-testid="settings-tools-bash-warning">{t('settingsTools.bash.warning')}</p>
    </div>
  )
}

// A tool switched by a boolean setting of the page (the web tools).
function BoolControls({ tool, settings, onChange }) {
  const field = FIELD_BY_KEY[tool.setting]
  const labelId = useId()
  const { t } = useTranslation('admin')
  if (!field || field.kind !== 'bool') return null
  return (
    <div className="st-tools__switch">
      <Switch checked={!!getValue(field, settings)} labelledBy={labelId} onChange={(on) => onChange(setValue(field, settings, on))} testId={`settings-tools-${tool.id}-switch`} />
      <span id={labelId}>{t('settingsTools.switch')}</span>
    </div>
  )
}

// The Test search of the web_search row: runs the query with each provider of
// the saved settings (POST /api/chats/admin/web/test) and shows what each did,
// so an administrator sees which engine challenges this server and why.
function WebSearchTest() {
  const { t } = useTranslation('admin')
  const inputId = useId()
  const [query, setQuery] = useState('LocalAI')
  const [state, setState] = useState({ busy: false, result: null, error: '' })
  const run = () => {
    setState({ busy: true, result: null, error: '' })
    chatBuiltinToolsApi.testWebSearch(query.trim() || 'LocalAI')
      .then(result => setState({ busy: false, result, error: '' }))
      .catch(e => setState({ busy: false, result: null, error: e.message || 'error' }))
  }
  const r = state.result
  return (
    <div className="st-tools__choice" data-testid="settings-tools-web-test">
      <p className="st-row__desc">{t('settingsTools.webTest.intro')}</p>
      <div className="st-tools__switch">
        <label htmlFor={inputId} className="dk-sr-only">{t('settingsTools.webTest.queryLabel')}</label>
        <input id={inputId} className="dk-input" type="search" value={query} disabled={state.busy} onChange={e => setQuery(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') run() }} data-testid="settings-tools-web-test-query" />
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={run} disabled={state.busy} aria-busy={state.busy || undefined} data-testid="settings-tools-web-test-run">
          {state.busy ? t('settingsTools.webTest.running') : t('settingsTools.webTest.run')}
        </button>
      </div>
      {state.error && <p className="st-row__desc" data-tone="warn" role="alert" data-testid="settings-tools-web-test-failed">{t('settingsTools.webTest.failed', { error: state.error })}</p>}
      {r && (
        <div aria-live="polite" data-testid="settings-tools-web-test-result">
          <p className="st-row__desc" data-tone={r.ok ? undefined : 'warn'} data-testid="settings-tools-web-test-summary">
            {r.ok ? t('settingsTools.webTest.answered', { host: r.host }) : `${t('settingsTools.webTest.noneAnswered')} ${r.error || ''}`}
          </p>
          <ul className="st-row__desc">
            {(r.providers || []).map(p => (
              <li key={p.name} data-testid={`settings-tools-web-test-${p.name}`} data-outcome={p.outcome}>
                <code className="st-key">{p.name}</code>{' '}
                <span className="st-tag" data-tone={p.outcome === 'ok' ? undefined : 'warn'}>{t(`settingsTools.webTest.outcomes.${p.outcome}`, { defaultValue: p.outcome })}</span>
                {p.outcome === 'ok' && <> {t('settingsTools.webTest.results', { count: p.results })}</>}
                {p.reason && <> {p.reason}</>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// The add-ons that replace one tool or skill: mutually exclusive, the pinned
// one serves. titles, when given, maps an add-on ID to its title. With no pin
// the server's default serves: the add-on installed first, which is marked.
function ProviderChoice({ tool, settings, onChange, titles }) {
  const { t, i18n } = useTranslation('admin')
  const pins = readPins(settings)
  const pinned = pins[tool.id] || ''
  const first = tool.default || tool.candidates[0]
  const active = pinned && tool.candidates.includes(pinned) ? pinned : first
  const set = (id) => onChange(writePins(settings, { ...pins, [tool.id]: id }))
  return (
    <fieldset className="st-tools__choice" data-testid={`settings-tools-${tool.id}-providers`}>
      <legend className="st-row__desc">{t('settingsTools.replaceExplain', { tool: tool.id })}</legend>
      {!pinned && (
        <Callout tone="warn" testId={`settings-tools-${tool.id}-unpinned`}>
          {t('settingsTools.choose', { count: tool.candidates.length, tool: tool.id })}
        </Callout>
      )}
      {tool.candidates.map(id => (
        <label key={id} className="st-tools__radio">
          <input type="radio" name={`provider-${tool.id}`} value={id} checked={id === active} onChange={() => set(id)} />
          <span>
            {titles?.[id] || id}
            {tool.stopped?.includes(id) && (
              <span className="st-row__desc" data-testid={`settings-tools-${tool.id}-${id}-stopped`}> ({t('settingsTools.stopped')})</span>
            )}
            {tool.installed_at?.[id] && formatWhen(tool.installed_at[id], i18n.language) && (
              <span className="st-row__desc" data-testid={`settings-tools-${tool.id}-${id}-installed`}> {t('settingsTools.installedOn', { when: formatWhen(tool.installed_at[id], i18n.language) })}</span>
            )}
            {id === first && tool.candidates.length > 1 && (
              <span className="st-tag" data-testid={`settings-tools-${tool.id}-${id}-default`}> {t('settingsTools.defaultInstalledFirst')}</span>
            )}
          </span>
        </label>
      ))}
    </fieldset>
  )
}

export default function BuiltinToolsSection({ settings, onChange }) {
  const { t } = useTranslation('admin')
  const [state, setState] = useState({ loading: true, tools: [], skills: [], error: '' })
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let live = true
    setState(s => ({ ...s, loading: true, error: '' }))
    chatBuiltinToolsApi.list()
      .then(res => { if (live) setState({ loading: false, tools: res?.tools || [], skills: res?.skills || [], error: '' }) })
      .catch(e => { if (live) setState({ loading: false, tools: [], skills: [], error: e.message || 'error' }) })
    return () => { live = false }
  }, [reload])

  if (state.loading) return <SkeletonRows rows={2} />
  if (state.error) return <LoadError title={t('settingsTools.loadFailed')} error={state.error} onRetry={() => setReload(n => n + 1)} />
  if (state.tools.length === 0) return <p className="st-note" data-testid="settings-tools-empty">{t('settingsTools.empty')}</p>

  return (
    <div className="st-list st-tools" data-testid="settings-tools">
      <p className="st-note">{t('settingsTools.intro')}</p>
      {state.tools.map(tool => {
        const by = tool.provider === 'builtin'
          ? t('settingsTools.byBuiltin')
          : tool.provider ? t('settingsTools.byAddon', { name: tool.titles?.[tool.provider] || tool.provider }) : t('settingsTools.byNone')
        return (
          <div key={tool.id} className="st-row st-tools__row" data-wide data-tool={tool.id} data-testid={`settings-tools-row-${tool.id}`}>
            <div className="st-row__main">
              <div className="st-row__title">
                <span className="st-row__label">{t(`settingsTools.labels.${tool.id}`, { defaultValue: tool.label })}</span>
                <span className="st-tag" data-testid={`settings-tools-${tool.id}-provider`}>{by}</span>
              </div>
              <p className="st-row__desc">{t(`settingsTools.descriptions.${tool.id}`, { defaultValue: tool.description })}</p>
              <div className="st-row__meta"><code className="st-key">{tool.setting}</code></div>
              {tool.unavailable && <p className="st-row__desc" data-tone="warn" data-testid={`settings-tools-${tool.id}-unavailable`}>{t('settingsTools.unavailable', { reason: tool.unavailable })}</p>}
              {tool.id === 'bash'
                ? <BashControls tool={tool} settings={settings} onChange={onChange} />
                : <BoolControls tool={tool} settings={settings} onChange={onChange} />}
              {tool.note && <p className="st-row__desc" data-tone="warn" data-testid={`settings-tools-${tool.id}-note`}>{t('settingsTools.toolNotRunning')}</p>}
              {tool.candidates.length > 1 && <ProviderChoice tool={tool} settings={settings} onChange={onChange} titles={tool.titles} />}
              {tool.id === 'web_search' && <WebSearchTest />}
              {tool.id === 'bash' && tool.candidates.length === 0 && (
                <p className="st-row__desc"><Link to={SANDBOX_INSTALL_LINK} data-testid="settings-tools-install-sandbox">{t('settingsTools.installSandbox')}</Link></p>
              )}
            </div>
          </div>
        )
      })}
      {state.skills.length > 0 && <SkillRows skills={state.skills} settings={settings} onChange={onChange} />}
    </div>
  )
}

// The built-in skills that installed add-ons replace: who serves each now and,
// when several add-ons replace one, the choice.
function SkillRows({ skills, settings, onChange }) {
  const { t } = useTranslation('admin')
  return (
    <div className="st-tools__skills" data-testid="settings-tools-skills">
      <h3 className="st-row__label">{t('settingsTools.skillsTitle')}</h3>
      <p className="st-note">{t('settingsTools.skillsIntro')}</p>
      {skills.map(skill => {
        const titles = skill.titles || {}
        const by = skill.provider === 'builtin'
          ? t('settingsTools.byBuiltin')
          : t('settingsTools.byAddon', { name: titles[skill.provider] || skill.provider })
        return (
          <div key={skill.id} className="st-row st-tools__row" data-wide data-skill={skill.id} data-testid={`settings-tools-skill-${skill.id}`}>
            <div className="st-row__main">
              <div className="st-row__title">
                <span className="st-row__label">{t('settingsTools.skillLabel', { skill: skill.id })}</span>
                <span className="st-tag" data-testid={`settings-tools-skill-${skill.id}-provider`}>{by}</span>
              </div>
              {skill.note && <p className="st-row__desc" data-tone="warn" data-testid={`settings-tools-skill-${skill.id}-note`}>{t('settingsTools.skillNotRunning')}</p>}
              {skill.candidates.length > 1 && <ProviderChoice tool={skill} settings={settings} onChange={onChange} titles={titles} />}
            </div>
          </div>
        )
      })}
    </div>
  )
}
