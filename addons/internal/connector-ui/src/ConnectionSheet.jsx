/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// Add or edit a connection in the host's side sheet: one scrolling form with
// three sections (Connection, Who can talk to it, What it may do), Cancel and
// "Save and test" pinned at the foot. Every field the inline form had is here:
// the permission matrix sits behind the Custom preset, and the replies, the
// files and the check of what the form will do sit behind disclosures.
import { useEffect, useRef, useState } from 'react'
import SideSheet from '../../host-compat/src/components/SideSheet'
import { AddonScope } from './ConnectionPane'
import AnswerFields from './AnswerFields'
import AccessView from './AccessView'
import ReadinessList from './ReadinessList'
import ApprovalOptions from './ApprovalFields'
import FilesFields from './FilesFields'
import WhoFields from './WhoFields'
import AccessMatrix from './AccessMatrix'
import PermissionFields from './PermissionFields'
import NotifyField from './NotifyField'
import { Field, Notice, RadioCards, Select, TextInput } from './controls'
import { jumpTo } from './accessHooks'
import { applyPreset, standardTitles } from './connection'
import { chatNamesFrom, knownTargets, recentPeople } from './people'
import { listeningChannels } from './connector'

function Section({ id, title, children }) {
  return (
    <section className="cn-sheet__section" aria-labelledby={id} data-testid={`connector-section-${id.replace('cn-sec-', '')}`}>
      <h3 className="cn-sheet__label" id={id}>{title}</h3>
      {children}
    </section>
  )
}

// Three presets for the permissions, and the whole matrix behind Custom.
function Permissions({ p }) {
  const { form, setForm, capabilities, agent, preset, setPreset, s } = p
  const titles = standardTitles(capabilities, agent)
  const options = [
    { value: 'answer', label: s('conn.preset.answer'), description: s('conn.preset.answerHelp') },
    { value: 'standard', label: s('conn.preset.standard'), description: titles.length ? s('conn.preset.standardHelp', { list: titles.join(', ') }) : s('conn.preset.standardNone') },
    { value: 'custom', label: s('conn.preset.custom'), description: s('conn.preset.customHelp') },
  ]
  const choose = (value) => {
    setPreset(value)
    if (value !== 'custom') setForm(applyPreset(form, value, capabilities, agent))
  }
  return (
    <>
      <RadioCards name="cn-preset" legend={s('conn.preset.label')} value={preset} options={options} onChange={choose} testId="connector-preset" columns={3} />
      <p className="dk-hint" data-testid="connector-preset-note">{s('conn.preset.note')}</p>
      {preset === 'custom' && (
        <AccessMatrix form={form} setForm={(f) => { setPreset('custom'); setForm(f) }} kindInfo={p.kindInfo} capabilities={capabilities} preview={p.preview} dirty={p.dirty} s={s} ctx={p.fixCtx}
          agent={agent} paired={p.pairedList || form.paired || []}
          onReset={() => { setPreset('custom'); setForm(applyPreset(form, 'standard', capabilities, agent)) }} />
      )}
    </>
  )
}

export default function ConnectionSheet({ p }) {
  const { t, s, form, setForm, messaging, capabilities, kindInfo, agents, agentOptional, toolOnly, TokenFields, ExtraFields, savedItem, busy, error, fixedAgent } = p
  const editing = Boolean(form.id)
  const agent = agents.find((a) => a.id === form.agent_id)
  const bodyRef = useRef(null)
  const failRef = useRef(null)
  const [moreOpen, setMoreOpen] = useState(false)
  const [checkOpen, setCheckOpen] = useState(Boolean(p.openCheck))

  // "Fix token" opens the form on the token: the first password field, else the first field.
  // The sheet opens on its first field, not on a fix button of the status panel.
  useEffect(() => {
    const id = requestAnimationFrame(() => {
      const form = bodyRef.current
      const target = p.focusToken ? form?.querySelector('input[type="password"]') : null
      ;(target || form?.querySelector('input:not([type="radio"]):not([type="checkbox"])'))?.focus()
    })
    return () => cancelAnimationFrame(id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p.focusToken])
  useEffect(() => {
    if (!p.scrollToPermissions) return
    const id = setTimeout(() => document.querySelector('[data-testid="connector-section-may"]')?.scrollIntoView?.({ block: 'start' }), 80)
    return () => clearTimeout(id)
  }, [p.scrollToPermissions])

  // A failed test moves focus to its message, which is an alert: it is read at once and the Test again button is next.
  // The message sits at the end of the form and sticks above the buttons (the buttons are outside the add-on's styles).
  useEffect(() => { if (p.testFail) failRef.current?.focus() }, [p.testFail])

  const submitDisabled = busy || !p.canSubmit
  const footer = (
    <>
      <button type="button" className="dk-btn dk-btn--ghost" data-testid="connector-cancel" onClick={p.onClose}>{t('cancel')}</button>
      <button type="submit" form="cn-sheet-form" className="dk-btn dk-btn--primary" data-testid="connector-save" aria-busy={busy ? 'true' : undefined} disabled={submitDisabled}>
        {busy ? (p.testStep ? s('conn.pane.testing') : s('conn.sheet.saving')) : s('conn.sheet.saveAndTest')}
      </button>
    </>
  )
  return (
    <SideSheet wide title={editing ? t('edit') : t('new')} description={editing ? s('conn.sheet.editDesc') : s('conn.sheet.addDesc')}
      onClose={p.onClose} closeLabel={s('conn.sheet.close')} testId="connector-sheet" labelId="connector-sheet-title" footer={footer}>
      <AddonScope>
        <form id="cn-sheet-form" ref={bodyRef} className="cn-sheet" onSubmit={p.onSubmit} aria-label={editing ? t('edit') : t('new')} data-testid="connector-form">
          {error && <Notice tone="error" role="alert" testId="connector-sheet-error">{error}</Notice>}
          {!toolOnly && savedItem?.agent_id && (
            <p className="cn-muted cn-agentlink">
              <a href={p.agentHref} target={p.dirty ? '_blank' : undefined} rel={p.dirty ? 'noopener noreferrer' : undefined} data-testid="connector-agent-link">
                {s('agentLink', { name: p.agentName(savedItem.agent_id) })}
              </a>
            </p>
          )}
          {p.sameBot}

          <Section id="cn-sec-connection" title={s('conn.sheet.connection')}>
            {messaging && capabilities.length > 0 && <ReadinessList s={s} ctx={p.fixCtx} rowsInput={p.readyInput} isNew={!editing} />}
            <Field label={t('name')}>
              {(f) => <TextInput {...f} value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />}
            </Field>
            <TokenFields form={form} setForm={setForm} t={t} />
            {toolOnly ? null : fixedAgent ? (
              <div className="dk-field" data-testid="connector-agent-fixed">
                <span className="dk-label">{t('agent')}</span>
                <p className="cn-fixed">{s('conn.sheet.agentFixed', { name: p.agentName(form.agent_id) })}</p>
              </div>
            ) : (
              <Field label={t('agent')} hint={agentOptional ? s('agentOptionalHint') : undefined} optional={agentOptional}>
                {(f) => (
                  <Select {...f} data-testid="connector-agent" aria-label={t('agent')} value={form.agent_id} required={!agentOptional} onChange={(e) => setForm(p.withAgent(form, e.target.value))}>
                    <option value="">{agentOptional ? s('agentCredentialsOnly') : t('agentNone')}</option>
                    {agents.map((a) => (
                      <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('agentNoModel', { name: a.name })}</option>
                    ))}
                  </Select>
                )}
              </Field>
            )}
            {ExtraFields && <ExtraFields form={form} setForm={setForm} t={t} />}
          </Section>

          {messaging && (
            <Section id="cn-sec-who" title={s('conn.sheet.who')}>
              <WhoFields key={form.id || 'new'} form={form} setForm={setForm} t={t} s={s} confirmed={p.confirmed} setConfirmed={p.setConfirmed} isChannel={p.isChannelEntry} mixed={p.mixedList}
                onPaired={p.pairedChanged} recent={recentPeople(p.senderLog, form.allow)} chatNames={chatNamesFrom(p.senderLog)}
                hint={p.lists[0]?.hint ? t(p.lists[0].hint) : undefined} help={p.lists[0]?.help ? t(p.lists[0].help) : undefined} />
            </Section>
          )}

          {messaging && (
            <Section id="cn-sec-may" title={s('conn.sheet.may')}>
              {capabilities.length > 0 && <Permissions p={{ ...p, agent, s }} />}
              {/* The approval of proposed triggers sits under the row Propose triggers of the matrix; a host with no capability list has no such row, so it shows here. */}
              {capabilities.length === 0 && <ApprovalOptions form={form} setForm={setForm} s={s} />}
              <details className="cn-more" data-testid="connector-replies-more" open={moreOpen} onToggle={(e) => setMoreOpen(e.currentTarget.open)}>
                <summary>{s('conn.sheet.replies')}</summary>
                <div className="cn-more__body">
                  <AnswerFields key={form.id || 'new'} form={form} setForm={setForm} t={t} kindInfo={kindInfo}
                    places={capabilities.length > 0 && (
                      <PermissionFields form={form} setForm={setForm} s={s} chatNames={chatNamesFrom(p.senderLog)}
                        listening={listeningChannels(form, p.isChannelEntry, p.mixedList)} hearsAll={Boolean(form.accept_any) || p.mixedList}
                        onSwitchOn={() => { p.setPreset('custom'); setTimeout(() => jumpTo(['[data-testid="connector-cap-switch-talkback.channels"]', '[data-testid="connector-matrix"]']), 60) }} />
                    )}
                    notify={<NotifyField form={form} setForm={setForm} s={s} label={s('notify.label')} hint={t('notifyHelp')} inputMode={p.notifyMode}
                      known={knownTargets({ form, senders: p.senderLog, isChannel: p.isChannelEntry })} />}
                    sizes={<FilesFields form={form} setForm={setForm} kind={p.kindFiles} saved={form.files} s={s} />} />
                </div>
              </details>
              {capabilities.length > 0 && (
                <details className="cn-more" data-testid="connector-check-more" open={checkOpen} onToggle={(e) => setCheckOpen(e.currentTarget.open)}>
                  <summary>{s('conn.sheet.check')}</summary>
                  <div className="cn-more__body">
                    <AccessView binding={form} capabilities={capabilities} s={s} version={p.accessVersion} setForm={setForm} preview={p.preview} kindInfo={kindInfo} ctx={p.fixCtx}
                      paired={p.pairedList || form.paired || []} isChannel={p.isChannelEntry} mixed={p.mixedList} chatNames={chatNamesFrom(p.senderLog)} senders={p.senderLog}
                      onAllowed={p.onAllowed} />
                  </div>
                </details>
              )}
            </Section>
          )}
          {p.dupAsk}
          {p.testFail && (
            <div className="cn-sheet__fail" ref={failRef} tabIndex={-1} data-testid="connector-test-failed">
              <Notice tone="error" role="alert">
                <strong className="ui-callout__title">{s('conn.sheet.testFailed')}</strong>
                <span className="cn-sheet__fail-reason">{p.testFail.reason || s('conn.sheet.testFailedNoReason')}</span>
                <span className="cn-row__actions">
                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-test-again" disabled={busy} aria-busy={busy ? 'true' : undefined} onClick={p.onTestAgain}>
                    {busy ? s('conn.pane.testing') : s('conn.sheet.testAgain')}
                  </button>
                </span>
              </Notice>
            </div>
          )}
          <span className="cn-vh" role="status">{busy && p.testStep ? s('conn.pane.testing') : ''}</span>
        </form>
      </AddonScope>
    </SideSheet>
  )
}
