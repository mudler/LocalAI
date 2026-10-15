import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

const WORD = { strong: 'ok', likely: 'ok', close: 'warn', near: 'warn', far: '' }

// The answer in a sentence: a headline, a word for how strong it is, and one
// plain line with the real numbers. `tone` picks the icon: match, miss, none.
export default function Verdict({ tone, title, word, children, testId = 'verdict' }) {
  const { t } = useTranslation('biometrics')
  return (
    <div className="idn-verdict" data-tone={tone} data-testid={testId} role="status">
      <span className="idn-verdict__icon" aria-hidden="true"><Icon name={tone === 'match' ? 'check' : tone === 'miss' ? 'help-circle' : 'minus'} /></span>
      <div className="idn-verdict__body">
        <h3 className="idn-verdict__title">
          {title}
          {word && <span className={`dk-badge${WORD[word] ? ` dk-badge--${WORD[word]}` : ''}`}>{t(`word.${word}`)}</span>}
        </h3>
        <p className="idn-verdict__text">{children}</p>
      </div>
    </div>
  )
}
