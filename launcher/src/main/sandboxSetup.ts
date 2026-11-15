// Runs the sandbox profile setup and removal through pkexec. The desktop's own
// dialog asks for the password; the launcher never sees it. Every command is an
// argv array and is never passed to a shell.
//
// Two steps: first `prepareSetup` or `prepareRemove` return the exact text and
// commands as data, so the caller can show them. Only after the user agrees
// does the caller pass the same plan to `runPlan`.

import { spawn } from 'node:child_process'
import { buildRemovePlan, buildSetupPlan, type SandboxPlan } from './sandboxProfile'

export interface RunResult {
  /** Exit code, or null when the process did not start or was killed by a signal. */
  exitCode: number | null
  stderr: string
  /** Error code of a failed spawn, for example `ENOENT` when the program is missing. */
  spawnError?: string
}

/** Starts a program from an argv array and waits for it. `input` goes to standard input. */
export type Runner = (argv: readonly string[], input?: string) => Promise<RunResult>

export type SetupStatus =
  | 'ok'
  /** The user closed the authentication dialog (pkexec exit 126). */
  | 'dismissed'
  /** The user is not authorised or authentication failed (pkexec exit 127). */
  | 'not-authorised'
  | 'pkexec-missing'
  | 'failed'

export interface SetupResult {
  status: SetupStatus
  /** Steps that finished with success before the run stopped. */
  completedSteps: number
  /** Short text for a failure, taken from the failing step. */
  detail?: string
}

export function prepareSetup(attachPattern: string): SandboxPlan {
  return buildSetupPlan(attachPattern)
}

export function prepareRemove(): SandboxPlan {
  return buildRemovePlan()
}

/** Runs the steps in order and stops at the first failure. Never throws. */
export async function runPlan(plan: SandboxPlan, run: Runner): Promise<SetupResult> {
  let done = 0
  for (const step of plan.steps) {
    if (step.argv[0] !== 'pkexec') return { status: 'failed', completedSteps: done, detail: 'step does not use pkexec' }
    let r: RunResult
    try {
      r = await run(step.argv, step.input)
    } catch (err) {
      return { status: 'failed', completedSteps: done, detail: (err as Error).message }
    }
    if (r.spawnError === 'ENOENT') return { status: 'pkexec-missing', completedSteps: done }
    if (r.spawnError) return { status: 'failed', completedSteps: done, detail: r.spawnError }
    if (r.exitCode === 126) return { status: 'dismissed', completedSteps: done }
    if (r.exitCode === 127) return { status: 'not-authorised', completedSteps: done }
    if (r.exitCode !== 0) {
      const tail = r.stderr.trim().split('\n').slice(-3).join('\n')
      return {
        status: 'failed',
        completedSteps: done,
        detail: tail || `${step.description} failed (exit ${r.exitCode ?? 'signal'})`,
      }
    }
    done++
  }
  return { status: 'ok', completedSteps: done }
}

/** The real runner. No shell. */
export const spawnRunner: Runner = (argv, input) =>
  new Promise((resolve) => {
    const [cmd, ...args] = argv
    let stderr = ''
    let settled = false
    const finish = (r: RunResult) => {
      if (!settled) {
        settled = true
        resolve(r)
      }
    }
    const child = spawn(cmd, args, { shell: false, stdio: ['pipe', 'ignore', 'pipe'] })
    child.stderr.on('data', (d: Buffer) => {
      stderr = (stderr + d.toString()).slice(-4096)
    })
    child.on('error', (err: NodeJS.ErrnoException) => finish({ exitCode: null, stderr, spawnError: err.code ?? 'ERROR' }))
    child.on('close', (code) => finish({ exitCode: code, stderr }))
    child.stdin.on('error', () => {})
    child.stdin.end(input ?? '')
  })
