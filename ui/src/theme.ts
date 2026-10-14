import { definePreset } from '@openvue/themes'
import Aura from '@openvue/themes/aura'

// Aura with teal as the interaction colour. Red is kept for one meaning
// only, in style.css: an attacker obtained a shell.
export const Cauri = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{teal.50}', 100: '{teal.100}', 200: '{teal.200}', 300: '{teal.300}', 400: '{teal.400}',
      500: '{teal.500}', 600: '{teal.600}', 700: '{teal.700}', 800: '{teal.800}', 900: '{teal.900}', 950: '{teal.950}',
    },
  },
})
