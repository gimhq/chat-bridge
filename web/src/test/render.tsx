import type { ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterContextProvider } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import { TooltipProvider } from '@/shared/components/ui/tooltip'

/** Renders ui with a query client and a bare router, so components with Links render synchronously. */
export function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const router = createRouter({ routeTree: createRootRoute(), history: createMemoryHistory() })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <RouterContextProvider router={router}>
          <TooltipProvider>{ui}</TooltipProvider>
        </RouterContextProvider>
      </QueryClientProvider>,
    ),
  }
}

export interface ApiCall {
  method: string
  path: string
  body: unknown
}

type Handler = (body: unknown, url: URL) => unknown

/** Routes fetch by "METHOD /path" to canned JSON; undefined answers 204, unknown routes 404. */
export function mockApi(routes: Record<string, Handler>) {
  const calls: ApiCall[] = []
  const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'http://localhost')
    const method = init?.method ?? 'GET'
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) as unknown : init?.body
    calls.push({ method, path: url.pathname, body })
    const handler = routes[`${method} ${url.pathname}`]
    if (!handler)
      return new Response(JSON.stringify({ error: { code: 'not_found', message: `no mock for ${method} ${url.pathname}` } }), { status: 404 })
    const out = handler(body, url)
    if (out instanceof Response)
      return out
    if (out === undefined)
      return new Response(null, { status: 204 })
    return new Response(JSON.stringify(out), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
  return { calls, spy }
}
