// Day-level date filters: kept in the URL as YYYY-MM-DD (local days), sent
// to the API as instants. "to" is inclusive in the UI, exclusive in the API.

export function toDay(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export function fromDay(s: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null
}

/** API bounds for a range of days: [start of first day, start of the day after the last). */
export function dayRange(from: string, to: string): { from?: string; to?: string } {
  const f = fromDay(from)
  const t = fromDay(to)
  if (t) t.setDate(t.getDate() + 1)
  return { from: f?.toISOString(), to: t?.toISOString() }
}
