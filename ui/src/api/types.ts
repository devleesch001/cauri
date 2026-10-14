// Mirrors the JSON of internal/store (Go). Timestamps are RFC 3339 strings.

export interface Page<T> {
  items: T[]
  total: number
  limit: number
  offset: number
}

export interface Sensor {
  id: string // the name the ingestor sends
  first_seen: string
  last_seen: string
}

export interface Credential {
  username: string
  password: string
  success: boolean
}

export interface Session {
  id: number
  sensor: string
  session: string // Cowrie's id
  src_ip: string | null
  src_port: number | null
  dst_ip: string | null
  dst_port: number | null
  protocol: string | null
  client_version: string | null
  hassh: string | null
  started_at: string
  ended_at: string | null
  duration_ms: number | null
  login_success: boolean
  tty_log: string | null
  tty_duration_ms: number | null
  tty_sha256: string | null
  tty_size: number | null
  geo_country: string | null // ISO 3166-1 alpha-2
  geo_city: string | null
  geo_source: string | null // GeoIP database used; set with no country: unknown address
  verdict: Verdict['verdict'] | null // stored; null until judged
  verdict_score: number | null
}

export interface SessionSummary extends Session {
  login_attempts: number
  commands: number
  tty_available: boolean
  credentials: Credential[]
}

export interface LoginAttempt {
  id: number
  session_id: number
  ts: string
  username: string
  password: string
  success: boolean
}

export interface Command {
  id: number
  session_id: number
  ts: string
  input: string
}

export interface Transfer {
  id: number
  session_id: number
  ts: string
  direction: 'download' | 'upload'
  url: string | null
  filename: string | null
  sha256: string | null
}

/** A Cowrie event: data is the object Cowrie wrote, unmodified; null once purged. */
export interface RawEvent {
  id: number
  eventid: string
  ts: string
  data: Record<string, unknown> | null
}

export interface SessionDetail extends SessionSummary {
  login_attempt_list: LoginAttempt[]
  command_list: Command[]
  transfers: Transfer[]
  events: RawEvent[]
}

export interface LoginAttemptRow {
  id: number
  session_id: number
  ts: string
  username: string
  password: string
  success: boolean
  sensor: string
  session: string
  src_ip: string | null
  client_version: string | null
  geo_country: string | null
}

/** A command of a replay and what it printed (GET /api/sessions/{id}/chapters). */
export interface Chapter {
  time: number // seconds from the start of the recording, real time
  command: string
  output: string // plain text, without the echo of the command or the prompt
  truncated: boolean
  full_screen: boolean // a full-screen program: no meaningful text output
}

export interface Chapters {
  records: number // records in the raw recording
  chapters: Chapter[]
}

/** Whether a session looks typed by a human or run by a bot (GET /api/sessions/{id}/verdict). */
export interface Verdict {
  verdict: 'human' | 'bot' | 'uncertain' | 'no_interaction'
  score: number
  reasons: { code: string; points: number }[]
}

export interface CountryCount {
  country: string // ISO 3166-1 alpha-2
  sessions: number
  shells: number // sessions that obtained a shell
}

/** GET /api/about. */
export interface About {
  version: string
  geoip: { type: string; built: string; provider: 'db-ip' | 'maxmind' | '' } | null
}
