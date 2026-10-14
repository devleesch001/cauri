// Where the markers of an asciicast v3 recording fall on the player's
// timeline. The player shortens every pause longer than idle_time_limit
// (asciinema-player's timeLimiter: each excess shifts all later events,
// markers included), so the real times of the commands do not match its
// clock unless the same shortening is applied here.

export function markerTimes(cast: string): number[] {
  const lines = cast.split('\n')
  let limit = Infinity
  try {
    const header = JSON.parse(lines[0] ?? '') as { idle_time_limit?: number }
    if (typeof header.idle_time_limit === 'number') limit = header.idle_time_limit
  } catch {
    return []
  }
  const times: number[] = []
  let time = 0 // real time, the sum of the intervals
  let shift = 0 // time removed by the idle limit so far
  for (const line of lines.slice(1)) {
    if (line === '' || line.startsWith('#')) continue
    let event: [number, string, unknown]
    try {
      event = JSON.parse(line) as [number, string, unknown]
    } catch {
      continue
    }
    const delay = event[0]
    time += delay
    if (delay > limit) shift += delay - limit
    if (event[1] === 'm') times.push(time - shift)
  }
  return times
}
