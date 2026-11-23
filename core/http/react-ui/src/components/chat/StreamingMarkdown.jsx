import { memo, useMemo } from 'react'
import { streamSegments } from '../../utils/streamMarkdown'
import { renderMarkdown } from '../../utils/markdown'
import { spaceSafeLinks } from '../../utils/chatMedia'
// eslint-disable-next-line no-unused-vars
import Prose from './Prose'

// A block of prose gets its own element so a flush that only extends the last
// block touches only that element: the earlier blocks keep their key and their
// DOM, and nothing already drawn is replaced. The text arrives as a string,
// not as the segment object, because streamSegments returns fresh objects on
// every call — a string lets memo bail out for a block that stopped growing.
// eslint-disable-next-line no-unused-vars
const ProseSegment = memo(function ProseSegment({ text, files, onOpenFile }) {
  const html = useMemo(() => renderMarkdown(spaceSafeLinks(text)), [text])
  return <Prose html={html} className="cx-seg" files={files} onOpenFile={onOpenFile} />
})

// A fenced block while the turn runs is one <code> with the source as a text
// child, so the browser grows a single Text node in place instead of the
// highlighter's tokens being thrown away and rebuilt on every flush. The
// language class only carries the fence's name, so the settled pass and the
// CSS agree; highlighting and Copy arrive when the turn settles.
// eslint-disable-next-line no-unused-vars
const CodeSegment = memo(function CodeSegment({ code, lang }) {
  return (
    <div className="cx-prose cx-seg">
      <pre><code className={lang ? `language-${lang}` : undefined}>{code}</code></pre>
    </div>
  )
})

// The reply while it is still arriving, drawn as segments: a completed prose
// block or a closed fence keeps its element, and only the trailing block grows.
export default function StreamingMarkdown({ text, files, onOpenFile }) {
  const segs = useMemo(() => streamSegments(text), [text])
  return (
    <div className="cx-prose cx-prose--live">
      {segs.map((seg) => (
        seg.kind === 'code'
          ? <CodeSegment key={seg.key} code={seg.code} lang={seg.lang} />
          : <ProseSegment key={seg.key} text={seg.text} files={files} onOpenFile={onOpenFile} />
      ))}
    </div>
  )
}
