// SPDX-License-Identifier: MIT
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

// The phone layout must never reach the desktop launcher, which shares the
// renderer: every rule of mobile.css is scoped under html.lnr-mobile (the
// class the app's entry sets). Colours come from the theme roles only.
const file = path.resolve(__dirname, '../src/mobile.css')
const css = fs.readFileSync(file, 'utf8')

/** The selectors of every style rule, at any depth of @media or @supports. */
function selectors(text: string): string[] {
  const src = text.replace(/\/\*[\s\S]*?\*\//g, '')
  const out: string[] = []
  let depth = 0
  let start = 0
  // The depth of each open block that holds rules (the top level and at-rules).
  const ruleBlocks = [0]
  for (let i = 0; i < src.length; i++) {
    const c = src[i]
    if (c === '{') {
      const prelude = src.slice(start, i).trim()
      depth++
      if (prelude.startsWith('@')) ruleBlocks.push(depth)
      else if (ruleBlocks[ruleBlocks.length - 1] === depth - 1) out.push(...splitList(prelude))
      start = i + 1
    } else if (c === '}') {
      if (ruleBlocks[ruleBlocks.length - 1] === depth) ruleBlocks.pop()
      depth--
      start = i + 1
    } else if (c === ';' && ruleBlocks[ruleBlocks.length - 1] === depth) {
      start = i + 1
    }
  }
  return out
}

/** Splits a selector list on the commas outside parentheses. */
function splitList(list: string): string[] {
  const parts: string[] = []
  let level = 0
  let from = 0
  for (let i = 0; i < list.length; i++) {
    if (list[i] === '(') level++
    else if (list[i] === ')') level--
    else if (list[i] === ',' && level === 0) {
      parts.push(list.slice(from, i).trim())
      from = i + 1
    }
  }
  parts.push(list.slice(from).trim())
  return parts.filter(Boolean)
}

describe('mobile.css', () => {
  it('has rules', () => {
    expect(selectors(css).length).toBeGreaterThan(10)
  })

  it('scopes every rule under html.lnr-mobile', () => {
    const loose = selectors(css).filter((s) => !/^html\.lnr-mobile(?=$|[\s.:[>+~])/.test(s))
    expect(loose).toEqual([])
  })

  it('finds an unscoped rule inside a media query (the check itself works)', () => {
    expect(selectors('@media (max-width: 399px) { .lnr-bar { color: red } }')).toEqual(['.lnr-bar'])
    expect(selectors('html.lnr-mobile .a, .b:is(.c, .d) { x: y }')).toEqual(['html.lnr-mobile .a', '.b:is(.c, .d)'])
  })

  it('has no literal colour', () => {
    const hits = css.split('\n').filter((line) => /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?)\(/.test(line))
    expect(hits).toEqual([])
  })
})
