import Icon from './Icon.jsx'
import { resolveStatus, STATUS_WORDS } from './status.js'

// One status chip: tone colour + icon + words, 22 px tall. Pass a status word
// from the vocabulary in status.js (running, starting, stopped, failed,
// update-available, ready, no-runs) and the chip finds its tone, icon and
// label. Pass `tone` and children for a chip outside the vocabulary
// ("Low disk", "Built-in").
//
//   status    a word of the vocabulary, or an alias (healthy, error, …)
//   tone      ok | warn | error | idle | accent, to override the status tone
//   label     text to use instead of the vocabulary's words
//   dot       a 6 px dot instead of the icon, for dense rows
//   icon      a kit icon id, to replace the tone's icon ("terminal")
//   words     how to say a vocabulary word: a function of its label key
//             (status.js), for a translated app. Default: STATUS_WORDS,
//             in English.
//
// One state per chip. Never "Off, starting".
const englishWord = (key) => STATUS_WORDS[key]

export default function StatusChip({ status, tone, label, dot = false, icon, words = englishWord, className = '', title, testId, children }) {
  const s = resolveStatus({ status, tone })
  const text = children ?? label ?? (s.label ? words(s.label) : status)
  return (
    <span className={`ui-chip ui-chip--${s.tone} ${className}`.trim()} data-status={status} title={title} data-testid={testId}>
      {dot
        ? <span className="ui-chip__dot" aria-hidden="true" />
        : <Icon name={icon || s.icon} spin={!icon && s.busy} className="ui-chip__icon" />}
      <span className="ui-chip__label">{text}</span>
    </span>
  )
}
