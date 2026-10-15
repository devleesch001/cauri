// The pages of the navigation bar, in order, shared by the bar and the
// search: a page added here appears in both. The statistics pages sit in
// a menu between the two lists.
export interface Page {
  /** Route name, also the key of its label (`nav.<label>`). */
  name: string
  label: string
  path: string
}

export const primaryPages: Page[] = [
  { name: 'overview', label: 'overview', path: '/' },
  { name: 'sessions', label: 'sessions', path: '/sessions' },
  { name: 'login-attempts', label: 'failedLogins', path: '/login-attempts' },
  { name: 'payloads', label: 'payloads', path: '/payloads' },
]

export const statsPages: Page[] = [
  { name: 'activity', label: 'activity', path: '/activity' },
  { name: 'credentials', label: 'credentials', path: '/credentials' },
  { name: 'commands', label: 'commands', path: '/commands' },
]

export const trailingPages: Page[] = [{ name: 'admin', label: 'admin', path: '/admin' }]
