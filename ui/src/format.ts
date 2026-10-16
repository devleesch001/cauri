import { t } from './i18n'

// Display helpers. Times are shown in the browser's time zone, in an
// unambiguous year-month-day 24-hour form, whatever the UI language.

const dateTime = new Intl.DateTimeFormat('sv-SE', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit',
})

export function formatTime(iso: string | null | undefined): string {
  return iso ? dateTime.format(new Date(iso)) : '—'
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return '—'
  if (seconds < 1) return t('units.ms', { n: Math.round(seconds * 1000) })
  if (seconds < 60) return t('units.s', { n: seconds.toFixed(1) })
  const m = Math.floor(seconds / 60)
  if (m < 60) return t('units.minSec', { m, s: Math.round(seconds % 60) })
  return t('units.hourMin', { h: Math.floor(m / 60), m: m % 60 })
}

/** A duration given in milliseconds, as the API stores them. */
export function formatMillis(ms: number | null | undefined): string {
  return formatDuration(ms === null || ms === undefined ? ms : ms / 1000)
}

/** Offset of at from start, as "+1.2 s". */
export function formatOffset(start: string, at: string): string {
  const s = (new Date(at).getTime() - new Date(start).getTime()) / 1000
  return '+' + (s < 60 ? t('units.s', { n: s.toFixed(1) }) : formatDuration(s))
}

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—'
  if (n < 1024) return t('units.bytes', { n })
  if (n < 1024 * 1024) return t('units.kilobytes', { n: (n / 1024).toFixed(1) })
  if (n < 1024 * 1024 * 1024) return t('units.megabytes', { n: (n / 1024 / 1024).toFixed(1) })
  return t('units.gigabytes', { n: (n / 1024 / 1024 / 1024).toFixed(2) })
}

export function endpoint(ip: string | null, port: number | null): string {
  if (!ip) return '—'
  return port ? `${ip}:${port}` : ip
}

const regions = new Intl.DisplayNames(['en'], { type: 'region' })

/** Name of a country from its ISO 3166-1 alpha-2 code. */
export function countryName(code: string): string {
  try {
    return regions.of(code) ?? code
  } catch {
    return code
  }
}

