// Display helpers. Times are shown in the browser's time zone, in an
// unambiguous year-month-day 24-hour form.

const dateTime = new Intl.DateTimeFormat('sv-SE', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit',
})

export function formatTime(iso: string | null | undefined): string {
  return iso ? dateTime.format(new Date(iso)) : '—'
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return '—'
  if (seconds < 1) return `${Math.round(seconds * 1000)} ms`
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m} min ${Math.round(seconds % 60)} s`
  return `${Math.floor(m / 60)} h ${m % 60} min`
}

/** Offset of t from start, as "+1.2 s". */
export function formatOffset(start: string, t: string): string {
  const s = (new Date(t).getTime() - new Date(start).getTime()) / 1000
  return '+' + (s < 60 ? `${s.toFixed(1)} s` : formatDuration(s))
}

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export function endpoint(ip: string | null, port: number | null): string {
  if (!ip) return '—'
  return port ? `${ip}:${port}` : ip
}
