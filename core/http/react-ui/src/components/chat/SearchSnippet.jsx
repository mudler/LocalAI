import { highlightParts } from '../../utils/chatSearch'

// The snippet of a search hit with the words that matched marked. The parts
// are text nodes and <mark> elements: message text is never read as HTML.
export default function SearchSnippet({ text, ranges }) {
  return highlightParts(text, ranges).map((p, i) => (
    p.mark ? <mark key={i} className="cx-find-mark">{p.text}</mark> : p.text
  ))
}
