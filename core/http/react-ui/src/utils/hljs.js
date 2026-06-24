// Curated highlight.js build.
//
// `import hljs from 'highlight.js'` pulls in the full bundle — ~190 language
// grammars, ~893 KB raw / ~294 KB gzip, the single biggest item in the app
// bundle (measured). We render code blocks from chat/markdown/canvas only, and
// only ever for a handful of common languages, so we import the lightweight
// core and register just the grammars below. `highlightAuto` still works — it
// auto-detects among the registered set, which covers what an LLM realistically
// emits. Import hljs from THIS module, never directly from 'highlight.js'.
import hljs from 'highlight.js/lib/core'

import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import kotlin from 'highlight.js/lib/languages/kotlin'
import lua from 'highlight.js/lib/languages/lua'
import makefile from 'highlight.js/lib/languages/makefile'
import markdown from 'highlight.js/lib/languages/markdown'
import php from 'highlight.js/lib/languages/php'
import plaintext from 'highlight.js/lib/languages/plaintext'
import powershell from 'highlight.js/lib/languages/powershell'
import python from 'highlight.js/lib/languages/python'
import ruby from 'highlight.js/lib/languages/ruby'
import rust from 'highlight.js/lib/languages/rust'
import scss from 'highlight.js/lib/languages/scss'
import shell from 'highlight.js/lib/languages/shell'
import sql from 'highlight.js/lib/languages/sql'
import swift from 'highlight.js/lib/languages/swift'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

// Each grammar registers its own aliases (e.g. js→javascript, ts→typescript,
// yml→yaml, html→xml, sh→bash, py→python), so hljs.getLanguage('js') resolves.
const languages = {
  bash, c, cpp, csharp, css, diff, dockerfile, go, ini, java, javascript,
  json, kotlin, lua, makefile, markdown, php, plaintext, powershell, python,
  ruby, rust, scss, shell, sql, swift, typescript, xml, yaml,
}
for (const [name, lang] of Object.entries(languages)) {
  hljs.registerLanguage(name, lang)
}

export default hljs
