// What a bundler-less build that did not follow the SDK contract looks like:
// a bare import of React. The host has no import map, so the module fails to
// load and no second React is ever resolved for it.
import React from 'react'
export function createAddon() {
  return { Component: () => React.createElement('p', null, 'unreachable'), dispose() {} }
}
