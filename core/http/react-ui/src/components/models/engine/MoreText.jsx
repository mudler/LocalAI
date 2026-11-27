import { useTranslation } from 'react-i18next'
import { firstSentence } from './labels'

// A text that shows its first sentence and keeps the rest one click away,
// so a field or a fact reads as one line. The disclosure is the native
// <details>: the first sentence is its summary.
//
//   text       the whole text
//   className  of the outer element
//   lead       optional node before the text (a chip, a figure)
//   id, testId
export default function MoreText({ text, className = '', lead = null, id, testId }) {
  const { t } = useTranslation('modelEditor')
  const [first, rest] = firstSentence(text)
  if (!rest) {
    return <span className={`rt-more ${className}`.trim()} id={id} data-testid={testId}>{lead}{first}</span>
  }
  return (
    <details className={`rt-more rt-more--fold ${className}`.trim()} id={id} data-testid={testId}>
      <summary className="rt-more__summary">{lead}<span>{first}</span><span className="rt-more__mark">{t('engineSettings.more')}</span></summary>
      <span className="rt-more__rest">{rest}</span>
    </details>
  )
}
