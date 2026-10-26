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
  kex_id: number | null // its key exchange
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
  // GeoIP database that located it; set with no country: unknown address
  geo_source_type: string | null
  geo_source_built: string | null // build date of that database
  geo_lat: number | null // centre of the city
  geo_lon: number | null
  asn: number | null // autonomous system, with an ASN database
  as_org: string | null
  verdict: Verdict['verdict'] | null // stored; null until judged
  verdict_score: number | null
  shell: boolean | null // typed in an interactive shell; null without a recording read
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
  simulated: boolean // no recording: the commands, without output
  chapters: Chapter[]
}

/** Whether a session looks typed by a human or run by a bot (GET /api/sessions/{id}/verdict). */
export interface Verdict {
  verdict: 'human' | 'bot' | 'uncertain' | 'no_interaction'
  score: number
  reasons: { code: string; points: number }[]
}

/** A city sessions came from (GET /api/cities): its centre, from the GeoIP database. */
export interface CityCount {
  country: string
  city: string
  lat: number
  lon: number
  sessions: number
  logged_in: number
}

export interface CountryCount {
  country: string // ISO 3166-1 alpha-2
  sessions: number
  logged_in: number // sessions that logged in
}

/** GET /api/about. */
export interface About {
  /** Of the running API: a release tag or "dev", and how it was built. */
  version: { tag: string; commit: string; dirty: boolean; date: string }
  geoip: GeoDatabase | null
  asn: GeoDatabase | null
  /** Providers whose data is shown, loaded or stored: each requires its attribution. */
  providers: ('db-ip' | 'maxmind')[]
}

/** A GeoIP or ASN database: its provider requires an attribution. */
export interface GeoDatabase {
  type: string
  built: string
  provider: 'db-ip' | 'maxmind' | ''
}

/** Headline numbers (GET /api/stats/summary); previous: the period of equal length before. */
export interface Summary {
  sessions: number
  logged_in: number
  source_ips: number
  countries: number
  logins: number
  accepted: number
  previous: Omit<Summary, 'previous'> | null
}

/** When sessions start, in UTC (GET /api/stats/activity). */
export interface Activity {
  hours: { weekday: number; hour: number; sessions: number }[] // weekday 0 is Sunday
  days: { day: string; sessions: number; logged_in: number }[] // day: YYYY-MM-DD
}

export interface ValueCount {
  value: string
  count: number
}

export interface PairCount {
  username: string
  password: string
  count: number
  accepted: number
}

/** Logins tried (GET /api/stats/credentials). */
export interface Credentials {
  attempts: number
  accepted: number
  distinct_usernames: number
  distinct_passwords: number
  top_usernames: ValueCount[]
  top_passwords: ValueCount[]
  top_pairs: PairCount[]
  worked: PairCount[]
  password_lengths: { length: number; count: number }[] // the last length means "and more"
}

/** A file transferred, by digest (GET /api/stats/payloads). */
export interface Payload {
  sha256: string
  transfers: number
  downloads: number
  uploads: number
  sessions: number
  sources: number
  first: string
  last: string
  url: string | null
  filename: string | null
  countries: string[]
}

export interface Payloads {
  sessions: number
  files: number
  items: Payload[]
}

/** What attackers run (GET /api/stats/commands). */
export interface Commands {
  total: number
  distinct: number
  sessions: number
  top: { input: string; count: number; sessions: number; sources: number; last: string }[]
  hassh: { hassh: string; client: string; clients: number; sessions: number; sources: number }[]
}

/** Who attacks (GET /api/stats/origins). */
export interface Origins {
  sources: { ip: string; country: string | null; sessions: number; logged_in: number; last: string }[]
  networks: { asn: number; org: string; sessions: number; sources: number }[] // with an ASN database
  clients: { client: string; sessions: number; sources: number }[]
}

/** An ingestor token, as the administration pages show it. */
/** What deleting sessions removes, or would (GET/DELETE /api/admin/sessions). */
export interface PurgeCounts {
  sessions: number
  events: number
  login_attempts: number
  commands: number
  transfers: number
  recordings: number
  key_exchanges: number
  networks: number
  geo_sources: number
}

/** Raw Cowrie lines: how many, how large, the oldest one's time. */
export interface RawLines {
  lines: number
  bytes: number
  oldest: string | null
}

/** The database, what it holds and where its space goes (GET /api/admin/database). */
export interface AdminDatabase {
  engine: 'sqlite' | 'postgres'
  version: string
  schema: string
  size: number
  free: number // space a compaction gives back, 0 if unknown
  tables: { name: string; rows: number; bytes: number }[]
  raw: RawLines
  recordings: { files: number; bytes: number } | null
}

/** An option of the API, where its value came from (GET /api/admin/config). */
export interface ConfigOption {
  name: string
  env: string
  default: string
  value: string
  source: 'flag' | 'env' | 'default'
  usage: string
}

export interface AdminConfig {
  options: ConfigOption[]
  unknown_env: string[] // CAURI_ variables no option reads
}

/** A third-party database the administration downloads (GET /api/admin/downloads). */
export interface AdminDownload {
  name: 'geoip' | 'asn'
  path: string
  urls: string[] // tried in order
  loaded: GeoDatabase | null
  size: number | null // of the file
  modified: string | null
}

/** A background task of the administration (GET /api/admin/jobs/{id}). */
export interface AdminJob {
  id: number
  kind: 'purge-sessions' | 'purge-raw-events' | 'vacuum' | 'download-geoip' | 'download-asn'
  params: Record<string, unknown> | null
  status: 'running' | 'done' | 'failed' | 'canceled'
  cancelable: boolean
  started: string
  finished: string | null
  progress: { done: number; total: number }
  /** Counts, sizes, or for a download the database fetched. */
  result: Record<string, number> | GeoDatabase | null
  error?: string
}

export interface AdminToken {
  id: number
  sensor: string
  name: string
  created_at: string
  last_used_at: string | null
  revoked_at: string | null
}

/** A source address and what came from it (GET /api/attackers/ip). */
export interface Attacker {
  ip: string
  sessions: number
  logged_in: number // sessions that logged in
  logins: number // login attempts
  first_seen: string
  last_seen: string
  country: string | null
  city: string | null
  asn: number | null
  as_org: string | null
  // Sessions by stored verdict.
  humans: number
  bots: number
  uncertain: number
  no_interaction: number
}

/**
 * Addresses grouped by the smart grouping (GET /api/attackers/ip?group=true):
 * one block, sharing the chosen network, SSH client and key exchange. A
 * single address is a group of its own, its block the address.
 */
export interface AttackerGroup {
  block: string
  ips: number
  sessions: number
  logged_in: number
  logins: number
  first_seen: string
  last_seen: string
  country: string | null
  asn: number | null
  as_org: string | null
  client: string | null
  hassh: string | null
  humans: number
  bots: number
  uncertain: number
  no_interaction: number
  members: Attacker[] // the busiest first, at most 200
}

/** One attacker, with what tells it apart (GET /api/attackers/ip/{ip}). */
export interface AttackerDetail extends Attacker {
  sensors: string[]
  clients: { client: string; sessions: number }[]
  kex: KexUse[] // key exchanges it used, most used first
  public_keys: OfferedKey[]
}

/** A key exchange of an attacker, with its sessions. */
export interface KexUse {
  id: number
  hassh: string | null
  key_algs: string | null
  sessions: number
}

/** A public key offered to log in (cowrie.client.fingerprint). */
export interface OfferedKey {
  fingerprint: string
  type: string
  key: string
  sessions: number
}

/** A key exchange, the algorithms an SSH tool offers, and what came with it (GET /api/attackers/kex). */
export interface KexAttacker extends KexLists {
  id: number
  hassh: string | null
  sessions: number
  sources: number // distinct source addresses
  logged_in: number
  logins: number
  first_seen: string
  last_seen: string
  humans: number
  bots: number
  uncertain: number
  no_interaction: number
}

/** The algorithm lists a client offered, each joined by commas as SSH sends it. */
export interface KexLists {
  kex_algs: string | null
  enc_algs: string | null
  mac_algs: string | null
  comp_algs: string | null
  key_algs: string | null // host key algorithms: what tells apart the combinations of one HASSH
  lang_algs: string | null
}

/** One key exchange, with what it links to (GET /api/attackers/kex/{id}). */
export interface KexDetail extends KexAttacker {
  sensors: string[]
  clients: { client: string; sessions: number }[]
  addresses: { ip: string; country: string | null; sessions: number; logged_in: number }[]
  variants: { id: number; key_algs: string | null; sessions: number }[] // the other combinations of its HASSH
  public_keys: OfferedKey[]
}
