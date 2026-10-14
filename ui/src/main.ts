import { createApp } from 'vue'
import { createPinia } from 'pinia'
import OpenVue from 'openvue/config'
import Tooltip from 'openvue/tooltip'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import './style.css'

import App from './App.vue'
import { router } from './router'
import { Cauri } from './theme'

createApp(App)
  .use(createPinia())
  .use(router)
  .use(OpenVue, { theme: { preset: Cauri, options: { darkModeSelector: 'system' } } })
  .directive('tooltip', Tooltip)
  .mount('#app')
