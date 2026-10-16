<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { geoEqualEarth, geoPath } from 'd3-geo'
import { feature } from 'topojson-client'
import type { FeatureCollection, Geometry } from 'geojson'
import type { GeometryCollection, Topology } from 'topojson-specification'
import world from '@/assets/world.json'
import type { CityCount, CountryCount } from '@/api/types'
import Button from 'openvue/button'
import MinusIcon from '@openvue/icons/minus'
import PlusIcon from '@openvue/icons/plus'
import UndoIcon from '@openvue/icons/undo'
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

// Zoom and pan: the map is drawn once, in a group moved and scaled as a
// whole (borders keep their width, cities their size on screen). The
// wheel, the buttons, the keys and a pinch zoom; dragging moves.
// translate is in map units, and the map never leaves the frame.
const MAX_ZOOM = 12
const zoom = ref(1)
const shift = ref<[number, number]>([0, 0])
const transform = computed(() => `translate(${shift.value[0]} ${shift.value[1]}) scale(${zoom.value})`)
const svg = ref<SVGSVGElement | null>(null)

function clamp(k: number, [x, y]: [number, number]): [number, number] {
  return [Math.min(0, Math.max(width * (1 - k), x)), Math.min(top * (1 - k), Math.max(bottom * (1 - k), y))]
}
/** Map coordinates of a point of the screen. */
function toMap(clientX: number, clientY: number): [number, number] {
  const m = svg.value?.getScreenCTM()
  if (!m) return [width / 2, (top + bottom) / 2]
  const p = new DOMPoint(clientX, clientY).matrixTransform(m.inverse())
  return [p.x, p.y]
}
/** Zooms to k keeping the map point `at` where it is on screen. */
function zoomTo(k: number, at: [number, number]) {
  const next = Math.min(MAX_ZOOM, Math.max(1, k))
  const f = next / zoom.value
  shift.value = clamp(next, [at[0] - (at[0] - shift.value[0]) * f, at[1] - (at[1] - shift.value[1]) * f])
  zoom.value = next
}
const centre = (): [number, number] => [width / 2, (top + bottom) / 2]
const zoomBy = (factor: number) => zoomTo(zoom.value * factor, centre())
function reset() {
  zoom.value = 1
  shift.value = [0, 0]
}
function wheel(e: WheelEvent) {
  zoomTo(zoom.value * Math.exp(-e.deltaY * 0.0015), toMap(e.clientX, e.clientY))
}

// Pointers on the map: one drags, two pinch.
const pointers = new Map<number, [number, number]>()
let moved = false
let pinch = 0
function down(e: PointerEvent) {
  pointers.set(e.pointerId, [e.clientX, e.clientY])
  moved = false
  pinch = 0
}
function drag(e: PointerEvent) {
  const last = pointers.get(e.pointerId)
  if (!last) return
  const here: [number, number] = [e.clientX, e.clientY]
  const ctm = svg.value?.getScreenCTM()
  if (!ctm) return
  if (pointers.size === 2) {
    const [a, b] = [...pointers.values()]
    const was = Math.hypot(a![0] - b![0], a![1] - b![1])
    pointers.set(e.pointerId, here)
    const [c, d] = [...pointers.values()]
    const now = Math.hypot(c![0] - d![0], c![1] - d![1])
    if (was > 0) zoomTo(zoom.value * (now / was), toMap((c![0] + d![0]) / 2, (c![1] + d![1]) / 2))
    moved = true
    pinch = now
    return
  }
  const dx = here[0] - last[0]
  const dy = here[1] - last[1]
  // A click that slides a little is still a click.
  if (!moved && Math.hypot(dx, dy) < 3) return
  // Captured only once it is a drag: a plain click must reach its country.
  if (!moved) (e.currentTarget as Element).setPointerCapture(e.pointerId)
  moved = true
  hover.value = null
  pointers.set(e.pointerId, here)
  shift.value = clamp(zoom.value, [shift.value[0] + dx / ctm.a, shift.value[1] + dy / ctm.d])
}
function up(e: PointerEvent) {
  pointers.delete(e.pointerId)
  if (!pointers.size) pinch = 0
}
/** A drag ends in a click on whatever is under the pointer: not a selection. */
function swallowClick(e: MouseEvent) {
  if (moved || pinch) {
    e.stopPropagation()
    moved = false
  }
}
function key(e: KeyboardEvent) {
  const step = 40 / zoom.value
  const moves: Record<string, [number, number]> = {
    ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step],
  }
  if (e.key === '+' || e.key === '=') zoomBy(1.5)
  else if (e.key === '-' || e.key === '_') zoomBy(1 / 1.5)
  else if (e.key === '0') reset()
  else if (moves[e.key] && zoom.value > 1) {
    const [dx, dy] = moves[e.key]!
    shift.value = clamp(zoom.value, [shift.value[0] + dx * zoom.value, shift.value[1] + dy * zoom.value])
  } else return
  e.preventDefault()
}

// What the pointer is on: a country, or a city of it. It changes only when
// the pointer enters another one, so moving over the map does not render
// its few hundred shapes again.
const hover = shallowRef<CountryCount | CityCount | null>(null)
const box = ref<HTMLElement | null>(null)
const tip = ref<HTMLElement | null>(null)
function at(e: PointerEvent, c?: CountryCount | CityCount) {
  const next = c ?? null
  if (hover.value !== next) hover.value = next
  // The tooltip follows the pointer outside Vue, by a transform on its own
  // layer: neither a render nor a repaint of the map beneath it.
  const r = box.value?.getBoundingClientRect()
  if (r && tip.value) tip.value.style.transform = `translate(${e.clientX - r.left + 12}px, ${e.clientY - r.top + 12}px)`
}
function move(e: PointerEvent, a2?: string) {
  at(e, a2 ? byCode.value.get(a2) : undefined)
}
const hoveredCity = computed(() => (hover.value && 'city' in hover.value ? hover.value : null))
function select(a2?: string) {
  if (a2 && byCode.value.has(a2)) emit('select', a2)
}
</script>

<template>
  <div
    ref="box"
    class="map"
    tabindex="0"
    :aria-label="t('overview.map.keys')"
    @keydown="key"
  >
    <svg
      ref="svg"
      :viewBox="viewBox"
      role="img"
      :aria-label="t('overview.map.label', { n: countries.length }, countries.length)"
      @pointerleave="hover = null"
      @pointerdown="down"
      @pointermove="drag"
      @pointerup="up"
      @pointercancel="up"
      @wheel.prevent="wheel"
      @click.capture="swallowClick"
    >
      <g
        class="world"
        :transform="transform"
      >
        <g class="countries">
          <path
            v-for="s in shapes"
            :key="s.key"
            :d="s.d"
            :style="{ fill: fill(s.a2) }"
            :class="{ active: s.a2 && byCode.has(s.a2), hovered: hover && !hoveredCity && hover.country === s.a2 }"
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
            :r="d.r / zoom"
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
      </g>
    </svg>
    <div
      class="controls"
      role="group"
      :aria-label="t('overview.map.zoom')"
    >
      <Button
        severity="secondary"
        size="small"
        :aria-label="t('overview.map.zoomIn')"
        :title="t('overview.map.zoomIn')"
        :disabled="zoom >= MAX_ZOOM"
        @click="zoomBy(1.5)"
      >
        <template #icon>
          <PlusIcon />
        </template>
      </Button>
      <Button
        severity="secondary"
        size="small"
        :aria-label="t('overview.map.zoomOut')"
        :title="t('overview.map.zoomOut')"
        :disabled="zoom <= 1"
        @click="zoomBy(1 / 1.5)"
      >
        <template #icon>
          <MinusIcon />
        </template>
      </Button>
      <Button
        severity="secondary"
        size="small"
        :aria-label="t('overview.map.resetZoom')"
        :title="t('overview.map.resetZoom')"
        :disabled="zoom === 1"
        @click="reset"
      >
        <template #icon>
          <UndoIcon />
        </template>
      </Button>
    </div>
    <div
      v-show="hover"
      ref="tip"
      class="tip"
      aria-hidden="true"
    >
      <template v-if="hover">
        <CountryFlag :code="hover.country">
          {{ hoveredCity ? `, ${hoveredCity.city}` : '' }}
        </CountryFlag>
        <span>{{ t('overview.sessions', { n: hover.sessions.toLocaleString() }, hover.sessions) }}</span>
        <span
          v-if="hover.logged_in"
          class="logged-in"
        >{{ t('overview.loggedIn', { n: hover.logged_in.toLocaleString() }, hover.logged_in) }}</span>
      </template>
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
  width: 100%;
  height: 100%;
}

/* Its own layer: repaints elsewhere on the page (the live feed, hovered
   cards) do not draw its detailed borders again. */
svg {
  display: block;
  width: 100%;
  height: 100%;
  cursor: grab;
  will-change: transform;
  touch-action: none;
}

svg:active {
  cursor: grabbing;
}

.map:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: -2px;
}

/* Above the scale: bottom right. */
.controls {
  position: absolute;
  right: var(--space-3);
  bottom: calc(var(--space-3) + 2rem);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.controls :deep(.p-button) {
  width: 2.2rem;
  height: 2.2rem;
  background: color-mix(in srgb, var(--c-surface) 92%, transparent);
  border: 1px solid var(--c-rule);
  box-shadow: 0 2px 10px rgb(0 0 0 / 8%);
  color: var(--c-ink);
}

.controls :deep(.p-button:disabled) {
  color: var(--c-muted);
  opacity: 0.55;
}

/* On a phone the page scrolls: a finger moving up or down scrolls it, the
   map is zoomed with the buttons. */
@media (width <= 900px) {
  svg {
    touch-action: pan-y;
  }
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

/* Dark on the coloured countries, never red: red means a login. */
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
  top: 0;
  left: 0;
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
  will-change: transform;
  white-space: nowrap;
}

.legend {
  position: absolute;
  right: var(--space-3);
  bottom: var(--space-3);
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
