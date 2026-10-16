import { definePreset } from '@openvue/themes'
import Aura from '@openvue/themes/aura'

// Aura with orange as the interaction colour. Red is kept for one meaning
// only, in style.css: an attacker logged in.
export const Cauri = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{orange.50}', 100: '{orange.100}', 200: '{orange.200}', 300: '{orange.300}', 400: '{orange.400}',
      500: '{orange.500}', 600: '{orange.600}', 700: '{orange.700}', 800: '{orange.800}', 900: '{orange.900}', 950: '{orange.950}',
    },
  },
})
