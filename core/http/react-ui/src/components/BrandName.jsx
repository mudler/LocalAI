import './brand-name.css'
import { useBranding, isDefaultName } from '../contexts/BrandingContext'

// The instance name, as text. Only the bundled name is drawn with its "AI" in
// the brand colour; any name an operator sets is plain text, so a rebranded
// instance shows nothing LocalAI-branded. The colour is a theme role
// (--color-brand-ai in theme.css): teal on light, yellow on dark.
export default function BrandName() {
  const { instanceName } = useBranding()
  if (!isDefaultName(instanceName)) return instanceName
  return (
    <>
      {instanceName.slice(0, -2)}
      <span className="brand-ai" data-testid="brand-ai">{instanceName.slice(-2)}</span>
    </>
  )
}
