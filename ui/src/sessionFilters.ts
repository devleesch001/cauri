import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { dayRange } from '@/dates'
import { queryValue } from '@/router'

/** URL query keys of the session filters (SessionFilters), besides the sensor of the header. */
export const sessionFilterKeys = [
  'ip', 'username', 'password', 'client', 'country', 'city', 'file', 'asn', 'command', 'hassh',
  'login', 'verdict', 'has_commands', 'shell', 'from', 'to',
]

/** The API parameters of the session filters in the URL, for a list and its export. */
export function useSessionFilterParams() {
  const route = useRoute()
  const q = (k: string) => queryValue(route.query, k)
  return computed(() => ({
    sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
    client: q('client'), country: q('country'), city: q('city'), file: q('file'), asn: q('asn'),
    command: q('command'), hassh: q('hassh'), login: q('login'), verdict: q('verdict'),
    has_commands: q('has_commands'), shell: q('shell'),
    ...dayRange(q('from'), q('to')),
  }))
}
