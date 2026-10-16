import { computed } from 'vue'
import { useRoute } from 'vue-router'
import type { DataTablePageEvent, DataTableSortEvent } from 'openvue/datatable'
import { patchQuery, queryValue } from '@/router'

// The sort and page size of a paged table, done by the API and kept in the
// URL like the filters: sort names a column (the list's default when
// absent), order is asc or desc (the default), rows is one of pageSizes.
// They are not filters: clearing the filters keeps them.
export function useTableSort(def: string) {
  const route = useRoute()
  const sort = computed(() => queryValue(route.query, 'sort'))
  const order = computed(() => queryValue(route.query, 'order'))
  /** Parameters of the list call. */
  const params = computed(() => ({ sort: sort.value || undefined, order: order.value || undefined }))
  /** The query to keep when the filters are cleared. */
  const kept = computed(() =>
    Object.fromEntries(Object.entries({ ...params.value, rows: queryValue(route.query, 'rows') }).filter(([, v]) => v)),
  )
  /** Props of the DataTable. */
  const field = computed(() => sort.value || def)
  const direction = computed(() => (order.value === 'asc' ? 1 : -1))

  function onSort(e: DataTableSortEvent) {
    const by = typeof e.sortField === 'string' ? e.sortField : def
    void patchQuery({ sort: by === def ? undefined : by, order: e.sortOrder === 1 ? 'asc' : undefined })
  }
  return { params, kept, field, direction, onSort }
}

/** Page sizes offered under a paged table. */
export const pageSizes = [10, 25, 50, 100]

// The page of a paged table and its size, def rows unless chosen.
export function useTablePage(def: number) {
  const route = useRoute()
  const rows = computed(() => {
    const n = Number(queryValue(route.query, 'rows'))
    return pageSizes.includes(n) ? n : def
  })
  const page = computed(() => Math.max(Number(queryValue(route.query, 'page')) || 1, 1))
  /** limit and offset of the list call. */
  const params = computed(() => ({ limit: rows.value, offset: (page.value - 1) * rows.value }))

  function onPage(e: DataTablePageEvent) {
    void patchQuery({ page: e.page > 0 ? String(e.page + 1) : undefined, rows: e.rows === def ? undefined : String(e.rows) }, true)
  }
  return { rows, page, params, onPage }
}

/** Query keys that are not filters. */
export const notFilters = ['page', 'rows', 'sort', 'order']
