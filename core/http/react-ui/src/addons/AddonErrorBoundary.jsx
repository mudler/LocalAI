import { Component } from 'react'

// A boundary around every add-on component and every layer it opens. A throw
// in an add-on draws this card and nothing else: the host page, the other
// add-ons and the navigation keep working. `labels` come from the shell so the
// text follows the interface language.
export default class AddonErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { error: null }
    this.retry = () => this.setState({ error: null })
  }

  static getDerivedStateFromError(error) {
    return { error }
  }

  componentDidCatch(error) {
    this.props.onError?.(error)
  }

  render() {
    if (!this.state.error) return this.props.children
    const { title, text, retryLabel } = this.props.labels || {}
    const { closeLabel, onClose } = this.props
    return (
      <div className="ad-boundary" role="alert" data-testid="addon-error">
        <h2 className="ad-boundary__title">{title}</h2>
        <p className="ad-boundary__text">{text}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={this.retry}>{retryLabel}</button>
        {onClose && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onClose}>{closeLabel}</button>}
      </div>
    )
  }
}
