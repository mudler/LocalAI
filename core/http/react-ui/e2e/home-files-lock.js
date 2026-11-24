import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// The specs that write the Home files of the test user (home.json, ui.json
// and the data files of the widgets) share one server and one user. When two
// of them run at the same time in two workers, one deletes or replaces the
// files of the other. A spec file that writes these files holds this lock from
// its first test to its last.
//
// The lock is a folder: mkdir is atomic. Its name has the process id of the
// Playwright runner, the parent of every worker, so one run never sees the
// lock of another. A lock older than staleMs is left over by a worker that
// died, and is taken over.

const dir = path.join(os.tmpdir(), `localai-e2e-home-files-${process.ppid}`)
const staleMs = 10 * 60_000
let held = false

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

export async function lockHomeFiles() {
  for (;;) {
    try {
      fs.mkdirSync(dir)
      held = true
      return
    } catch (err) {
      if (err.code !== 'EEXIST') throw err
    }
    try {
      if (Date.now() - fs.statSync(dir).mtimeMs > staleMs) fs.rmSync(dir, { recursive: true, force: true })
    } catch {
      // The holder removed it in the meantime: try again.
    }
    await sleep(250)
  }
}

export function unlockHomeFiles() {
  if (!held) return
  held = false
  fs.rmSync(dir, { recursive: true, force: true })
}

// holdHomeFiles registers the hooks of a spec file: call it before the other
// hooks of the file, so the lock is taken first and released last.
export function holdHomeFiles(test) {
  test.beforeAll(async () => {
    // Waiting for another spec file is not a failure of this one.
    test.setTimeout(staleMs)
    await lockHomeFiles()
  })
}
