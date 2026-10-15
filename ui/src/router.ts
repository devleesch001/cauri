import { createRouter, createWebHistory, type LocationQuery, type Router } from 'vue-router'

export const router: Router = createRouter({
  // The server answers index.html for every path outside /api.
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: () => import('./views/OverviewView.vue') },
    { path: '/activity', name: 'activity', component: () => import('./views/ActivityView.vue') },
    { path: '/credentials', name: 'credentials', component: () => import('./views/CredentialsView.vue') },
    { path: '/commands', name: 'commands', component: () => import('./views/CommandsView.vue') },
    { path: '/payloads', name: 'payloads', component: () => import('./views/PayloadsView.vue') },
    { path: '/sessions', name: 'sessions', component: () => import('./views/SessionsView.vue') },
    { path: '/sessions/:id', name: 'session', component: () => import('./views/SessionDetailView.vue'), props: true },
    { path: '/login-attempts', name: 'login-attempts', component: () => import('./views/LoginAttemptsView.vue') },
    { path: '/admin', name: 'admin', component: () => import('./views/AdminView.vue') },
    { path: '/:rest(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue') },
  ],
})

/** First value of a query parameter, or '' if absent. */
export function queryValue(query: LocationQuery, key: string): string {
  const v = query[key]
  return (Array.isArray(v) ? v[0] : v) ?? ''
}

/**
 * Merges patch into the query of the current route. Filters live in the
 * URL, so a filtered view can be shared or bookmarked. Changing a filter
 * returns to the first page.
 */
export function patchQuery(patch: Record<string, string | undefined>, keepPage = false) {
  const query: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...router.currentRoute.value.query, ...patch })) {
    const value = Array.isArray(v) ? v[0] : v
    if (value) query[k] = value
  }
  if (!keepPage) delete query.page
  return router.replace({ query })
}
