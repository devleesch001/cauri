import type { KexLists } from '@/api/types'

/** One algorithm list of a key exchange, as shown. */
export interface KexList {
  /** Key of its label (`attacker.lists.<name>`). */
  name: string
  /** Cowrie's name of the list. */
  cowrie: string
  /** Its algorithms, in the order offered: the client's preference. */
  algorithms: string[]
  /** Whether the HASSH hashes it. */
  hassh: boolean
}

// The six lists of a key exchange, those the HASSH hashes first.
export function kexLists(k: KexLists): KexList[] {
  const split = (v: string | null) => (v ? v.split(',').filter(Boolean) : [])
  return [
    { name: 'kex', cowrie: 'kexAlgs', algorithms: split(k.kex_algs), hassh: true },
    { name: 'enc', cowrie: 'encCS', algorithms: split(k.enc_algs), hassh: true },
    { name: 'mac', cowrie: 'macCS', algorithms: split(k.mac_algs), hassh: true },
    { name: 'comp', cowrie: 'compCS', algorithms: split(k.comp_algs), hassh: true },
    { name: 'key', cowrie: 'keyAlgs', algorithms: split(k.key_algs), hassh: false },
    { name: 'lang', cowrie: 'langCS', algorithms: split(k.lang_algs), hassh: false },
  ]
}
