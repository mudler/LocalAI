// Pure helpers that prepare an assistant reply for speech: strip markdown,
// split into chunks a speech model can take, and pick the reply text out of
// the transcript lines of a turn.

// Plain spoken text from markdown. Fenced code is dropped, inline code keeps
// its text, links keep their label, images vanish.
export function speechText(markdown) {
  let t = String(markdown ?? '')
  t = t.replace(/```[\s\S]*?(```|$)/g, ' ')
  t = t.replace(/~~~[\s\S]*?(~~~|$)/g, ' ')
  t = t.replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
  t = t.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
  t = t.replace(/`([^`]*)`/g, '$1')
  t = t.replace(/<[^>]+>/g, ' ')
  t = t.replace(/https?:\/\/[^\s<>)\]]+/g, ' ')
  t = t.replace(/^[ \t]*\|?[ \t]*:?-{2,}:?[ \t]*(\|[ \t]*:?-{2,}:?[ \t]*)*\|?[ \t]*$/gm, ' ')
  t = t.replace(/^[ \t]*#{1,6}[ \t]+/gm, '')
  t = t.replace(/^[ \t]*>+[ \t]?/gm, '')
  t = t.replace(/^[ \t]*([-*+]|\d+[.)])[ \t]+/gm, '')
  t = t.replace(/\|/g, ' ')
  t = t.replace(/(\*\*|__|~~)/g, '')
  t = t.replace(/(^|[^\w*])[*_]+|[*_]+(?=$|[^\w*])/g, '$1')
  t = t.replace(/\s+/g, ' ')
  return t.trim()
}

// Sentences, each with its trailing space, ended by ! ? … 。！？, a newline, or a
// dot before whitespace or the end (so 3.5 and example.com stay whole).
function sentences(text) {
  return text.match(/(?:[^.!?…。！？\n]|\.(?!\s|$))*(?:[!?…。！？]+|\.+(?=\s|$)|\n+|$)\s*/gu)?.filter((s) => s.trim() !== '') ?? []
}

// Splits text at sentence ends and packs sentences into chunks of at most
// max characters. A sentence longer than max is cut at the last space before
// max, or at max when it has no space.
export function chunkSpeech(text, max = 300) {
  const out = []
  let cur = ''
  const push = () => {
    const c = cur.trim()
    if (c) out.push(c)
    cur = ''
  }
  for (let s of sentences(String(text ?? ''))) {
    if (s.trim().length > max) {
      push()
      s = s.trim()
      while (s.length > max) {
        let cut = s.lastIndexOf(' ', max)
        if (cut <= 0) {
          cut = max
          // Do not split a surrogate pair.
          const c = s.charCodeAt(cut - 1)
          if (cut > 1 && c >= 0xd800 && c <= 0xdbff) cut--
        }
        const head = s.slice(0, cut).trim()
        if (head) out.push(head)
        s = s.slice(cut).trim()
      }
      cur = s
      continue
    }
    if (cur && (cur + s).trim().length > max) push()
    cur += s
  }
  push()
  return out
}

// The text of the assistant lines of the last turn after afterSeq, in order.
// Tool and reasoning lines are not spoken. The last turn starts after the last
// user line.
export function lastAssistantText(messages, afterSeq) {
  const lines = (messages || []).filter((m) => m && m.seq > afterSeq)
  let start = 0
  lines.forEach((m, i) => { if (m.role === 'user') start = i + 1 })
  return lines
    .slice(start)
    .filter((m) => m.role === 'assistant' && m.text)
    .map((m) => m.text)
    .join('\n')
}
