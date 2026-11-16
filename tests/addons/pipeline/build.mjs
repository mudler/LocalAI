import { createRequire } from 'node:module'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
const root = fileURLToPath(new URL('../../../', import.meta.url))
const require = createRequire(`${root}core/http/react-ui/package.json`)
const { build } = await import(require.resolve('rolldown'))
const dir = `${root}tests/addons/pipeline/dist`
mkdirSync(dir, { recursive: true })
await build({
  input: `${root}tests/addons/pipeline/host.jsx`,
  output: { file: `${dir}/app.js`, format: 'esm' },
  plugins: [{
    name: 'raw-svg',
    load(id) {
      if (id.endsWith('.svg?raw')) return `export default ${JSON.stringify(readFileSync(id.slice(0, -4), 'utf8'))}`
    },
  }],
  resolve: { alias: {
    react: `${root}core/http/react-ui/node_modules/react`,
    'react-dom': `${root}core/http/react-ui/node_modules/react-dom`,
  } },
  transform: {
    jsx: { runtime: 'automatic' },
    define: { 'process.env.NODE_ENV': '"development"' },
  },
})
const styles = ['../packages/ui-kit/vendor/tokens.css', '../packages/ui-kit/themes/localai.css', '../packages/ui-kit/vendor/motion.css', '../packages/ui-kit/vendor/components.css', 'index.css', 'theme.css', '../packages/ui-kit/css/kit.css', 'App.css'].map(path => readFileSync(`${root}core/http/react-ui/src/${path}`, 'utf8'))
styles.push(readFileSync(`${root}addons/asset-pipeline/ui/src/style.css`, 'utf8'))
writeFileSync(`${dir}/style.css`, styles.join('\n'))
writeFileSync(`${dir}/index.html`, '<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;background:#101922;color:#dce5ed;font-family:system-ui,sans-serif}</style><link rel="stylesheet" href="/style.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>')
