// Generates src/assets/world.json, the country borders of the world map,
// from Natural Earth (public domain), pinned to a release:
//
//   node scripts/world-geo.mjs
//
// The output is committed: the build does not download anything. Each
// country keeps only its ISO 3166-1 alpha-2 code ("a2"), the code the API
// gives; Antarctica is left out (no attacks come from it, and it takes a
// sixth of the height). 1:50m keeps small countries that 1:110m drops,
// such as Singapore and Hong Kong, large sources of attacks; the borders
// are then simplified to keep the file small.
import { writeFile } from 'node:fs/promises'
import { quantize } from 'topojson-client'
import { topology } from 'topojson-server'
import { presimplify, quantile, simplify } from 'topojson-simplify'

const source =
  'https://raw.githubusercontent.com/nvkelso/natural-earth-vector/v5.1.2/geojson/ne_50m_admin_0_countries.geojson'
const keep = Number(process.env.KEEP ?? 0.1) // share of points kept

const res = await fetch(source)
if (!res.ok) throw new Error(`${source}: ${res.status}`)
const ne = await res.json()

const features = ne.features
  .filter((f) => f.properties.ISO_A2_EH !== 'AQ')
  .map((f) => {
    // ISO_A2 is -99 for France, Norway and others; ISO_A2_EH fixes them.
    const a2 = f.properties.ISO_A2_EH
    return { type: 'Feature', geometry: f.geometry, properties: /^[A-Z]{2}$/.test(a2) ? { a2 } : {} }
  })

// Simplified unquantized, then quantized: simplify leaves plain coordinates
// with their weights, several times larger.
let topo = presimplify(topology({ countries: { type: 'FeatureCollection', features } }))
topo = quantize(simplify(topo, quantile(topo, keep)), 1e4)
const out = new URL('../src/assets/world.json', import.meta.url)
await writeFile(out, JSON.stringify(topo))
console.log(`${features.length} countries, ${JSON.stringify(topo).length} bytes`)
