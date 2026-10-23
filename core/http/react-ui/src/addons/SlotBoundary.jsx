import { Component } from 'react'

// The boundary of one contribution. Unlike the page boundary of an add-on
// route it draws a small inline marker: a slot is part of a host page.
export default class SlotBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { failed: false }
  }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error) {
    this.props.onError?.(error)
  }

  render() {
    if (!this.state.failed) return this.props.children
    return <span className="ad-slot-unavailable" role="status" data-testid="addon-slot-unavailable">{this.props.label}</span>
  }
}

