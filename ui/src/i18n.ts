import { createI18n } from 'vue-i18n'
import en from './locales/en'

// English is the only locale today. To add one: create locales/<code>.ts
// with the same keys (typed by MessageSchema), register it below, and pick
// the locale (for example from navigator.language).
export type MessageSchema = typeof en

export const i18n = createI18n<[MessageSchema], 'en'>({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: { en },
})

// Types the keys of t() and $t() in every component.
declare module 'vue-i18n' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  export interface DefineLocaleMessage extends MessageSchema {}
}

// For code outside components (formatting, timeline).
export const t = i18n.global.t
