/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The fields of a binding that say how the bot answers: what a sender sees
// while the agent works, which chat a message belongs to, and whether the
// files of a turn go out. The words are the same on every platform, so they
// live here as defaults; an add-on can still change one in its namespace.
import { CONVERSATIONS, MAX_WINDOW_MINUTES, REPLY_MODES } from './connector'

export const ANSWER_TEXT = {
  'answer.title': 'How it answers',
  'answer.replyLabel': 'While the agent works',
  'answer.reply.off': 'Typing, then the answer',
  'answer.reply.progress': 'Progress: the tools it uses',
  'answer.reply.stream': 'Live: the answer as it is written',
  'answer.replyHelp': 'Progress and Live show one working message, and the answer replaces it. On a platform with drafts, Live shows a private chat a draft instead, and the answer follows it. A long answer goes on in new messages.',
  'answer.conversationLabel': 'Conversations',
  'answer.conversation.sender': 'One chat per person',
  'answer.conversation.chat': 'One chat per group or channel',
  'answer.conversation.window': 'Per person, new after a quiet time',
  'answer.conversation.thread': 'New chat per message; replies go on',
  'answer.window': 'Quiet time, in minutes',
  'answer.files': 'Send the files the agent makes',
  'answer.filesHelp': 'After the answer, up to 5 files of at most 1 MB each. Programs and scripts are not sent.',
  'answer.summary': 'Answers: {{reply}} · {{conversation}}',
  'answer.short.off': 'typing',
  'answer.short.progress': 'progress',
  'answer.short.stream': 'live answer',
  'answer.short.sender': 'a chat per person',
  'answer.short.chat': 'a chat per group',
  'answer.short.window': 'new chat after {{count}} min',
  'answer.short.thread': 'a chat per message',
}

// text reads a string of the add-on's namespace, with the default above.
export function answerT(t) {
  return (key, opts = {}) => t(key, { defaultValue: ANSWER_TEXT[key], ...opts })
}

// answerSummary says in a few words how a binding answers, for its row.
export function answerSummary(c, t) {
  const s = answerT(t)
  return s('answer.summary', {
    reply: s(`answer.short.${c.reply_mode || 'progress'}`),
    conversation: s(`answer.short.${c.conversation || 'sender'}`, { count: c.window_minutes }),
  })
}

export default function AnswerFields({ form, setForm, t }) {
  const s = answerT(t)
  return (
    <fieldset className="cn-field cn-answer" data-testid="connector-answer">
      <legend>{s('answer.title')}</legend>
      <label className="cn-field">
        <span>{s('answer.replyLabel')}</span>
        <select className="input" data-testid="connector-reply" value={form.reply_mode || 'progress'}
          onChange={(e) => setForm({ ...form, reply_mode: e.target.value })}>
          {REPLY_MODES.map((m) => <option key={m} value={m}>{s(`answer.reply.${m}`)}</option>)}
        </select>
        <span className="cn-muted">{s('answer.replyHelp')}</span>
      </label>
      <label className="cn-field">
        <span>{s('answer.conversationLabel')}</span>
        <select className="input" data-testid="connector-conversation" value={form.conversation || 'sender'}
          onChange={(e) => setForm({ ...form, conversation: e.target.value })}>
          {CONVERSATIONS.map((m) => <option key={m} value={m}>{s(`answer.conversation.${m}`)}</option>)}
        </select>
      </label>
      {form.conversation === 'window' && (
        <label className="cn-field cn-answer__window">
          <span>{s('answer.window')}</span>
          <input className="input" type="number" inputMode="numeric" min={1} max={MAX_WINDOW_MINUTES} required data-testid="connector-window"
            value={form.window_minutes ?? ''} onChange={(e) => setForm({ ...form, window_minutes: e.target.value === '' ? '' : Number(e.target.value) })} />
        </label>
      )}
      <label className="cn-radio">
        <input type="checkbox" data-testid="connector-files" checked={form.send_files !== false}
          onChange={(e) => setForm({ ...form, send_files: e.target.checked })} />
        <span>{s('answer.files')}</span>
      </label>
      <span className="cn-muted">{s('answer.filesHelp')}</span>
    </fieldset>
  )
}
