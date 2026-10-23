// A host component reached through the SDK, as a component the page code can
// import by the name it knew. It is looked up when it is drawn, so the SDK that
// is current at that moment answers; a ref reaches the host component.
import { getSdk } from './runtime.js'

export function fromSdk(name) {
  const { React } = getSdk()
  const Component = React.forwardRef((props, ref) => {
    const Host = getSdk().ui[name]
    return React.createElement(Host, ref ? { ...props, ref } : props)
  })
  Component.displayName = `Host(${name})`
  return Component
}
