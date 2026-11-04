/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The fields of a binding that say how the bot answers: what a sender sees
// while the agent works, which chat a message belongs to, and whether the
// files of a turn go out. The words are the same on every platform, so they
// live here as defaults; an add-on can still change one in its namespace.
import { Field, Hint, Select, Switch, TextInput } from './controls'
import EN from './i18n/connector.en.json'
import { CONVERSATIONS, MAX_WINDOW_MINUTES, REPLY_LANGUAGES, REPLY_MODES } from './connector'

// text reads a string of the add-on's namespace, with the English default of
// the shared bundle (i18n/connector.en.json). The translations of that bundle
// are registered under the namespace "connector", which every page reads after
// its own, so an add-on can still change one word in its namespace.
export function answerT(t) {
  return (key, opts = {}) => t(key, { defaultValue: EN[key] ?? EN[`${key}_other`], ...opts })
}

// answerSummary says in a few words how a binding answers, for its row.
export function answerSummary(c, t) {
  const s = answerT(t)
  return s('answer.summary', {
    reply: s(`answer.short.${c.reply_mode || 'progress'}`),
    conversation: s(`answer.short.${c.conversation || 'sender'}`, { count: c.window_minutes }),
  })
}

export default function AnswerFields({ form, setForm, t, kindInfo }) {
  const s = answerT(t)
  const m = Number(form.window_minutes)
  const windowBad = form.conversation === 'window' && (!Number.isInteger(m) || m < 1 || m > MAX_WINDOW_MINUTES)
  return (
    <fieldset className="cn-group" data-testid="connector-answer">
      <legend>{s('answer.title')}</legend>
      <Field label={s('answer.replyLabel')} hint={s('answer.replyHelp')}>
        {(p) => (
          <Select {...p} data-testid="connector-reply" value={form.reply_mode || 'progress'}
            onChange={(e) => setForm({ ...form, reply_mode: e.target.value })}>
            {REPLY_MODES.map((x) => <option key={x} value={x}>{s(`answer.reply.${x}`)}</option>)}
          </Select>
        )}
      </Field>
      <Field label={s('answer.conversationLabel')} hint={s('answer.conversationHelp')}>
        {(p) => (
          <Select {...p} data-testid="connector-conversation" value={form.conversation || 'sender'}
            onChange={(e) => setForm({ ...form, conversation: e.target.value })}>
            {CONVERSATIONS.map((x) => <option key={x} value={x}>{s(`answer.conversation.${x}`)}</option>)}
          </Select>
        )}
      </Field>
      {form.conversation === 'window' && (
        <Field label={s('answer.window')} className="cn-answer__window"
          error={windowBad ? s('answer.windowRange', { max: MAX_WINDOW_MINUTES }) : undefined}>
          {(p) => (
            <TextInput {...p} type="number" inputMode="numeric" min={1} max={MAX_WINDOW_MINUTES} required data-testid="connector-window"
              value={form.window_minutes ?? ''} onChange={(e) => setForm({ ...form, window_minutes: e.target.value === '' ? '' : Number(e.target.value) })} />
          )}
        </Field>
      )}
      <Field label={s('answer.languageLabel')} hint={s('answer.languageHelp')}>
        {(p) => (
          <Select {...p} data-testid="connector-reply-language" value={form.reply_language || 'auto'}
            onChange={(e) => setForm({ ...form, reply_language: e.target.value })}>
            {REPLY_LANGUAGES.map((x) => <option key={x} value={x}>{s(`answer.language.${x}`)}</option>)}
          </Select>
        )}
      </Field>
      <div className="cn-switchfield">
        <Switch testId="connector-files" checked={form.send_files !== false} onChange={(on) => setForm({ ...form, send_files: on })}>
          {s('answer.files')}
        </Switch>
        <Hint>{s('answer.filesHelp')}</Hint>
      </div>
      {(kindInfo?.options || []).map((o) => (
        <Field key={o.id} label={o.title} hint={o.description}>
          {(p) => (
            <Select {...p} data-testid={`connector-option-${o.id}`} value={form.options?.[o.id] || o.default}
              onChange={(e) => setForm({ ...form, options: { ...form.options, [o.id]: e.target.value } })}>
              {o.values.map((v) => <option key={v.id} value={v.id}>{v.title}</option>)}
            </Select>
          )}
        </Field>
      ))}
    </fieldset>
  )
}
