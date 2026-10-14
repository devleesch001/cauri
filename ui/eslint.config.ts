import pluginVue from 'eslint-plugin-vue'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'

export default defineConfigWithVueTs(
  { ignores: ['node_modules/**'] },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
)
