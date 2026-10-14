import type { RawEvent } from './api/types'

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

function duration(d: Record<string, unknown>): string {
  if (typeof d.duration_ms === 'number') return `${(d.duration_ms / 1000).toFixed(1)} s`
  if (d.duration !== undefined) return `${Number(d.duration).toFixed(1)} s`
  return ''
}

function prose(e: RawEvent): ProseEntry {
  const d = e.data
  const p = (text: string, detail?: string, tone: Tone = 'normal'): ProseEntry => ({ kind: 'prose', ts: e.ts, text, detail, tone })
  switch (e.eventid) {
    case 'cowrie.session.connect':
      return p(`Connected to port ${str(d.dst_port)} over ${str(d.protocol) || 'ssh'} from`, `${str(d.src_ip)}:${str(d.src_port)}`)
    case 'cowrie.client.version':
      return p('Client announced', str(d.version))
    case 'cowrie.client.kex':
      return p('Key exchange, HASSH', str(d.hassh), 'muted')
    case 'cowrie.client.size':
      return p(`Terminal size ${str(d.width)}×${str(d.height)}`, undefined, 'muted')
    case 'cowrie.session.params':
      return p('Shell architecture', str(d.arch), 'muted')
    case 'cowrie.login.failed':
      return p('Login refused', `${str(d.username)} / ${str(d.password)}`)
    case 'cowrie.login.success':
      return p('Login accepted, shell opened', `${str(d.username)} / ${str(d.password)}`, 'shell')
    case 'cowrie.session.file_download':
      return p(`Downloaded ${str(d.url)}`, `sha256 ${str(d.shasum)}`, 'shell')
    case 'cowrie.session.file_download.failed':
      return p('Download failed', str(d.url))
    case 'cowrie.session.file_upload':
      return p(`Uploaded ${str(d.filename)}`, `sha256 ${str(d.shasum)}`, 'shell')
    case 'cowrie.log.closed':
      return p(`Terminal recording closed after ${duration(d)}`, undefined, 'muted')
    case 'cowrie.session.closed':
      return p(`Disconnected after ${duration(d)}`)
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
