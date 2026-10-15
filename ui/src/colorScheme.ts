import { ref, watchEffect } from 'vue'

// The colour scheme chosen by the viewer, remembered per browser. It is the
// one source of dark mode: the app-dark class on <html>, which the OpenVue
// theme (darkModeSelector) and style.css both read.
export type ColorScheme = 'light' | 'dark' | 'system'

const key = 'cauri.colorScheme'
const system = window.matchMedia('(prefers-color-scheme: dark)')

function stored(): ColorScheme {
  try {
    const v = localStorage.getItem(key)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // private mode or blocked storage: the default
  }
  return 'system'
}

export const colorScheme = ref<ColorScheme>(stored())

function apply() {
  const dark = colorScheme.value === 'dark' || (colorScheme.value === 'system' && system.matches)
  const root = document.documentElement
  root.classList.toggle('app-dark', dark)
  // Native parts too: scrollbars, form controls.
  root.style.colorScheme = dark ? 'dark' : 'light'
}

// Applied before the app mounts (imported by main.ts): no light flash.
watchEffect(() => {
  apply()
  try {
    localStorage.setItem(key, colorScheme.value)
  } catch {
    // see above
  }
})
system.addEventListener('change', apply)
