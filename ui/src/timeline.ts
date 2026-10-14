import type { RawEvent } from './api/types'
import { formatDuration } from './format'
import { t } from './i18n'

// Turns the raw Cowrie events of a session into timeline entries:
// consecutive commands are grouped into one terminal block, every other
// event becomes a line of prose.

export type Tone = 'normal' | 'muted' | 'shell'

export interface ProseEntry {
  kind: 'prose'
  ts: string
  text: string
  detail?: string // technical value, shown in the data face
  tone: Tone
}

export interface TerminalLine {
  ts: string
  input: string
  failed: boolean // Cowrie did not know the command
}

export interface TerminalEntry {
  kind: 'terminal'
  ts: string
  lines: TerminalLine[]
}

export type Entry = ProseEntry | TerminalEntry

const str = (v: unknown): string => (typeof v === 'string' ? v : v === undefined || v === null ? '' : String(v))

// Cowrie 3 writes duration_ms, older versions duration in seconds.
function duration(d: Record<string, unknown>): string {
  if (d.duration_ms !== undefined) return formatDuration(Number(d.duration_ms) / 1000)
  if (d.duration !== undefined) return formatDuration(Number(d.duration))
  return ''
}

function prose(e: RawEvent): ProseEntry {
  const d = e.data
  const p = (text: string, detail?: string, tone: Tone = 'normal'): ProseEntry => ({ kind: 'prose', ts: e.ts, text, detail, tone })
  switch (e.eventid) {
    case 'cowrie.session.connect':
      return p(t('events.connect', { port: str(d.dst_port), protocol: str(d.protocol) || 'ssh' }), `${str(d.src_ip)}:${str(d.src_port)}`)
    case 'cowrie.client.version':
      return p(t('events.clientVersion'), str(d.version))
    case 'cowrie.client.kex':
      return p(t('events.kex'), str(d.hassh), 'muted')
    case 'cowrie.client.size':
      return p(t('events.clientSize', { width: str(d.width), height: str(d.height) }), undefined, 'muted')
    case 'cowrie.client.fingerprint':
      return p(t('events.keyOffered', { user: str(d.username) }), `${str(d.type)} ${str(d.fingerprint)}`.trim(), 'muted')
    case 'cowrie.client.var':
      return p(t('events.clientVar'), `${str(d.name)}=${str(d.value)}`, 'muted')
    case 'cowrie.session.params':
      return p(t('events.params'), str(d.arch), 'muted')
    case 'cowrie.login.failed':
      return p(t('events.loginFailed'), `${str(d.username)} / ${str(d.password)}`)
    case 'cowrie.login.success':
      return p(t('events.loginSuccess'), `${str(d.username)} / ${str(d.password)}`, 'shell')
    case 'cowrie.session.file_download':
      return p(t('events.download', { url: str(d.url) }), t('session.files.sha256', { sha: str(d.shasum) }), 'shell')
    case 'cowrie.session.file_download.failed':
      return p(t('events.downloadFailed'), str(d.url))
    case 'cowrie.session.file_upload':
      return p(t('events.upload', { filename: str(d.filename) }), t('session.files.sha256', { sha: str(d.shasum) }), 'shell')
    case 'cowrie.log.closed':
      return p(t('events.logClosed', { duration: duration(d) }), undefined, 'muted')
    case 'cowrie.session.closed':
      return p(t('events.sessionClosed', { duration: duration(d) }))
    default:
      return p(str(d.message) || e.eventid, str(d.message) ? e.eventid : undefined, 'muted')
  }
}

export function timeline(events: RawEvent[]): Entry[] {
  const out: Entry[] = []
  for (const e of events) {
    if (e.eventid === 'cowrie.command.input' || e.eventid === 'cowrie.command.failed') {
      const line = { ts: e.ts, input: str(e.data.input), failed: e.eventid === 'cowrie.command.failed' }
      const last = out[out.length - 1]
      // command.failed repeats the input it follows: mark that line instead.
      if (line.failed && last?.kind === 'terminal') {
        const prev = last.lines[last.lines.length - 1]
        if (prev && prev.input === line.input) {
          prev.failed = true
          continue
        }
      }
      if (last?.kind === 'terminal') last.lines.push(line)
      else out.push({ kind: 'terminal', ts: e.ts, lines: [line] })
      continue
    }
    out.push(prose(e))
  }
  return out
}
