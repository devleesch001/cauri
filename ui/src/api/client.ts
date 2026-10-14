import type { LoginAttemptRow, Page, Sensor, SessionDetail, SessionSummary } from './types'

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
  sessions: (params: Params) => get<Page<SessionSummary>>('/sessions', params),
  session: (id: string) => get<SessionDetail>('/sessions/' + encodeURIComponent(id)),
  loginAttempts: (params: Params) => get<Page<LoginAttemptRow>>('/login-attempts', params),
}
