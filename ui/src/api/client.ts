import type {
  About,
  Activity,
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

async function get<T>(path: string, params: Params = {}): Promise<T> {
  const query = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') query.set(k, String(v))
  }
  const qs = query.toString()
  const res = await fetch(base + path + (qs ? '?' + qs : ''), { headers: { Accept: 'application/json' } })
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
  sessions: (params: Params) => get<Page<SessionSummary>>('/sessions', params),
  session: (id: string) => get<SessionDetail>('/sessions/' + encodeURIComponent(id)),
  chapters: (id: string) => get<Chapters>('/sessions/' + encodeURIComponent(id) + '/chapters'),
  verdict: (id: string) => get<Verdict>('/sessions/' + encodeURIComponent(id) + '/verdict'),
  /** URL of a session's replay; realTime removes the idle time limit. */
  castUrl: (id: string, realTime: boolean) =>
    `${base}/sessions/${encodeURIComponent(id)}/cast` + (realTime ? '?idle_time_limit=0' : ''),
  loginAttempts: (params: Params) => get<Page<LoginAttemptRow>>('/login-attempts', params),
}
