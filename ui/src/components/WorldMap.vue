<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { geoEqualEarth, geoPath } from 'd3-geo'
import { feature } from 'topojson-client'
import type { FeatureCollection, Geometry } from 'geojson'
import type { GeometryCollection, Topology } from 'topojson-specification'
import world from '@/assets/world.json'
import type { CityCount, CountryCount } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'

// Countries coloured by sessions, in the Equal Earth projection (areas
// kept true), and cities as circles whose area follows their sessions.
// Borders: Natural Earth 1:50m, see scripts/world-geo.mjs. The overlay
// slot gets the projection, for other markers.
const props = withDefaults(defineProps<{ countries: CountryCount[]; cities?: CityCount[] }>(), { cities: () => [] })
const emit = defineEmits<{ select: [code: string]; selectCity: [code: string, city: string] }>()
const { t } = useI18n()

const topo = world as unknown as Topology<{ countries: GeometryCollection<{ a2?: string }> }>
const land = feature(topo, topo.objects.countries) as FeatureCollection<Geometry, { a2?: string }>

const width = 960
const projection = geoEqualEarth().fitWidth(width, land)
const path = geoPath(projection)
const [[, top], [, bottom]] = path.bounds(land)
const viewBox = `0 ${top} ${width} ${bottom - top}`
const shapes = land.features.map((f, i) => ({ key: f.properties.a2 ?? `x${i}`, a2: f.properties.a2, d: path(f) ?? '' }))

/** Projects a longitude and latitude to map coordinates, for overlays. */
function project(lon: number, lat: number): [number, number] | null {
  return projection([lon, lat])
}

const byCode = computed(() => new Map(props.countries.map((c) => [c.country, c])))
const max = computed(() => props.countries.reduce((m, c) => Math.max(m, c.sessions), 0))

// Logarithmic: a few countries send most attacks and would leave every
// other one pale on a linear scale.
function share(n: number): number {
  return max.value > 1 ? Math.log(n) / Math.log(max.value) : 1
}
function fill(a2?: string): string | undefined {
  const c = a2 ? byCode.value.get(a2) : undefined
  if (!c) return undefined
  return `color-mix(in oklab, var(--p-primary-color) ${Math.round(18 + 82 * share(c.sessions))}%, var(--c-map-land))`
}

// Largest first, so that smaller cities stay on top and can be pointed.
const dots = computed(() => {
  const most = props.cities.reduce((m, c) => Math.max(m, c.sessions), 0)
  return props.cities
    .map((c) => ({ c, at: projection([c.lon, c.lat]), r: 1.5 + 10 * Math.sqrt(c.sessions / most) }))
    .filter((d): d is { c: CityCount; at: [number, number]; r: number } => d.at !== null)
    .sort((a, b) => b.r - a.r)
})

// What the pointer is on: a country, or a city of it.
const hover = ref<{ c: CountryCount | CityCount; x: number; y: number } | null>(null)
const box = ref<HTMLElement | null>(null)
function at(e: PointerEvent, c?: CountryCount | CityCount) {
  const r = box.value?.getBoundingClientRect()
  hover.value = c && r ? { c, x: e.clientX - r.left, y: e.clientY - r.top } : null
}
function move(e: PointerEvent, a2?: string) {
  at(e, a2 ? byCode.value.get(a2) : undefined)
}
const hoveredCity = computed(() => (hover.value && 'city' in hover.value.c ? hover.value.c : null))
function select(a2?: string) {
  if (a2 && byCode.value.has(a2)) emit('select', a2)
}
</script>

<template>
  <div
    ref="box"
    class="map"
  >
    <svg
      :viewBox="viewBox"
      role="img"
      :aria-label="t('overview.map.label', { n: countries.length }, countries.length)"
      @pointerleave="hover = null"
    >
      <g class="countries">
        <path
          v-for="s in shapes"
          :key="s.key"
          :d="s.d"
          :style="{ fill: fill(s.a2) }"
          :class="{ active: s.a2 && byCode.has(s.a2), hovered: hover && !hoveredCity && hover.c.country === s.a2 }"
          @pointermove="move($event, s.a2)"
          @click="select(s.a2)"
        />
      </g>
      <g class="cities">
        <circle
          v-for="d in dots"
          :key="`${d.c.country}/${d.c.city}`"
          :cx="d.at[0]"
          :cy="d.at[1]"
          :r="d.r"
          :class="{ hovered: hoveredCity === d.c }"
          @pointermove="at($event, d.c)"
          @click="emit('selectCity', d.c.country, d.c.city)"
        />
      </g>
      <g class="overlay">
        <slot
          name="overlay"
          :project="project"
        />
      </g>
    </svg>
    <div
      v-if="hover"
      class="tip"
      :style="{ left: `${hover.x}px`, top: `${hover.y}px` }"
      aria-hidden="true"
    >
      <CountryFlag :code="hover.c.country">
        {{ hoveredCity ? `, ${hoveredCity.city}` : '' }}
      </CountryFlag>
      <span>{{ t('overview.sessions', { n: hover.c.sessions.toLocaleString() }, hover.c.sessions) }}</span>
      <span
        v-if="hover.c.shells"
        class="shell"
      >{{ t('overview.shells', { n: hover.c.shells.toLocaleString() }, hover.c.shells) }}</span>
    </div>
    <div class="legend muted">
      <span>1</span>
      <span class="ramp" />
      <span>{{ max.toLocaleString() }}</span>
      <span>{{ t('overview.map.legend') }}</span>
    </div>
  </div>
</template>

<style scoped>
.map {
  position: relative;
}

svg {
  display: block;
  width: 100%;
  height: auto;
}

path {
  fill: var(--c-map-land);
  stroke: var(--c-surface);
  stroke-width: 0.5;
  vector-effect: non-scaling-stroke;
}

path.active {
  cursor: pointer;
}

path.hovered {
  stroke: var(--c-ink);
  stroke-width: 1.2;
}

/* Dark on the coloured countries, never red: red means a shell. */
circle {
  fill: var(--c-ink);
  fill-opacity: 0.4;
  stroke: var(--c-surface);
  stroke-width: 0.6;
  vector-effect: non-scaling-stroke;
  cursor: pointer;
}

circle.hovered {
  fill-opacity: 0.75;
  stroke: var(--c-ink);
}

.tip {
  position: absolute;
  z-index: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  background: var(--c-surface);
  border: 1px solid var(--c-rule);
  border-radius: 6px;
  box-shadow: 0 2px 8px rgb(0 0 0 / 12%);
  pointer-events: none;
  transform: translate(12px, 12px);
  white-space: nowrap;
}

.legend {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.85em;
}

.ramp {
  width: 8rem;
  height: 0.6rem;
  border-radius: 2px;
  background: linear-gradient(
    to right,
    color-mix(in oklab, var(--p-primary-color) 18%, var(--c-map-land)),
    var(--p-primary-color)
  );
}
</style>
