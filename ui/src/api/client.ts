import type {
  About,
  Activity,
  Attacker,
  AttackerDetail,
  KexAttacker,
  KexDetail,
  AdminConfig,
  AdminDatabase,
  AdminJob,
  AdminToken,
  PurgeCounts,
  RawLines,
  Chapters,
  CityCount,
  Commands,
  CountryCount,
  Credentials,
  LoginAttemptRow,
  Origins,
  Page,
  Payloads,
  Sensor,
  SessionDetail,
  SessionSummary,
  Summary,
  Verdict,
} from './types'

// Paths are relative to the page: the API and the UI share a domain, so
// there is no API URL to configure and no CORS.
const base = '/api'

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

export type Params = Record<string, string | number | boolean | undefined | null>

/** The URL of an API path with its parameters; empty ones are left out. */
function url(path: string, params: Params = {}): string {
  const query = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') query.set(k, String(v))
  }
  const qs = query.toString()
  return base + path + (qs ? '?' + qs : '')
}

async function get<T>(path: string, params: Params = {}): Promise<T> {
  const res = await fetch(url(path, params), { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let message = res.statusText
    try {
      message = ((await res.json()) as { error?: string }).error ?? message
    } catch {
      // not JSON: keep the status text
    }
    throw new ApiError(res.status, message)
  }
  return (await res.json()) as T
}

/**
 * Sends a change to the administration routes: JSON in, JSON or nothing
 * out. The header proves the request comes from this UI (see the API).
 */
async function send<T>(method: string, path: string, body?: unknown, params: Params = {}): Promise<T> {
  const res = await fetch(url(path, params), {
    method,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-Cauri-Admin': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let message = res.statusText
    try {
      message = ((await res.json()) as { error?: string }).error ?? message
    } catch {
      // not JSON: keep the status text
    }
    throw new ApiError(res.status, message)
  }
  return (res.status === 204 ? undefined : await res.json()) as T
}

/** The administration routes; a 401 means signed out. */
export const admin = {
  login: (password: string) => send<void>('POST', '/admin/login', { password }),
  logout: () => send<void>('POST', '/admin/logout'),
  /** Resolves if signed in, rejects with a 401 otherwise. */
  session: () => fetch(base + '/admin/session').then((r) => (r.ok ? undefined : Promise.reject(new ApiError(r.status, r.statusText)))),
  tokens: () => get<AdminToken[]>('/admin/tokens'),
  createToken: (sensor: string, name: string) =>
    send<{ token: AdminToken; secret: string }>('POST', '/admin/tokens', { sensor, name }),
  revokeToken: (id: number) => send<void>('POST', `/admin/tokens/${id}/revoke`),
  database: () => get<AdminDatabase>('/admin/database'),
  config: () => get<AdminConfig>('/admin/config'),
  /** What deleting the sessions the filters select would remove; nothing changes. */
  simulateSessions: (filters: Params) => get<PurgeCounts>('/admin/sessions', filters),
  /** Starts deleting them: the same filters as the simulation. */
  purgeSessions: (filters: Params) => send<AdminJob>('DELETE', '/admin/sessions', undefined, filters),
  simulateRaw: (days: number) => get<RawLines>('/admin/raw-events', { older_than_days: days }),
  purgeRaw: (days: number) => send<AdminJob>('DELETE', '/admin/raw-events', undefined, { older_than_days: days }),
  vacuum: () => send<AdminJob>('POST', '/admin/database/vacuum'),
  jobs: () => get<AdminJob[]>('/admin/jobs'),
  job: (id: number) => get<AdminJob>(`/admin/jobs/${id}`),
  cancelJob: (id: number) => send<AdminJob>('POST', `/admin/jobs/${id}/cancel`),
}

export const api = {
  sensors: () => get<Sensor[]>('/sensors'),
  countries: (params: Params) => get<CountryCount[]>('/countries', params),
  cities: (params: Params) => get<CityCount[]>('/cities', params),
  summary: (params: Params) => get<Summary>('/stats/summary', params),
  activity: (params: Params) => get<Activity>('/stats/activity', params),
  credentials: (params: Params) => get<Credentials>('/stats/credentials', params),
  payloads: (params: Params) => get<Payloads>('/stats/payloads', params),
  origins: (params: Params) => get<Origins>('/stats/origins', params),
  commands: (params: Params) => get<Commands>('/stats/commands', params),
  about: () => get<About>('/about'),
  attackers: (params: Params) => get<Page<Attacker>>('/attackers/ip', params),
  attacker: (ip: string, params: Params) => get<AttackerDetail>('/attackers/ip/' + encodeURIComponent(ip), params),
  kexAttackers: (params: Params) => get<Page<KexAttacker>>('/attackers/kex', params),
  kexAttacker: (id: string, params: Params) => get<KexDetail>('/attackers/kex/' + encodeURIComponent(id), params),
  sessions: (params: Params) => get<Page<SessionSummary>>('/sessions', params),
  session: (id: string) => get<SessionDetail>('/sessions/' + encodeURIComponent(id)),
  /** simulated asks for the commands alone, even where a recording exists. */
  chapters: (id: string, simulated = false) =>
    get<Chapters>('/sessions/' + encodeURIComponent(id) + '/chapters', simulated ? { simulated: 1 } : {}),
  verdict: (id: string) => get<Verdict>('/sessions/' + encodeURIComponent(id) + '/verdict'),
  /** URL of a session's replay; realTime removes the idle time limit, simulated asks for the commands alone. */
  castUrl: (id: string, realTime: boolean, simulated = false) =>
    url(`/sessions/${encodeURIComponent(id)}/cast`, {
      idle_time_limit: realTime ? 0 : undefined,
      simulated: simulated ? 1 : undefined,
    }),
  loginAttempts: (params: Params) => get<Page<LoginAttemptRow>>('/login-attempts', params),
  /** CSV downloads of the lists, with the same filters (no page). */
  sessionsCsvUrl: (params: Params) => url('/sessions.csv', params),
  loginAttemptsCsvUrl: (params: Params) => url('/login-attempts.csv', params),
}
