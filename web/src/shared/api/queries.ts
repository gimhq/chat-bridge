import type {
  Account,
  ApiEvent,
  Attachment,
  BridgeStatus,
  Chat,
  ChatRequest,
  Contact,
  LoginStep,
  Message,
  Page,
  Person,
  PersonSuggestion,
  Platform,
  RequestAction,
  RequestState,
  ResolvedChat,
  SendRequest,
  Webhook,
} from './types'
import { keepPreviousData, useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, seg } from '@/shared/lib/http'

const acc = (a: string) => `/accounts/${seg(a)}`

export const qk = {
  status: ['status'] as const,
  platforms: ['platforms'] as const,
  accounts: ['accounts'] as const,
  account: (a: string) => ['accounts', a] as const,
  login: (a: string) => ['login', a] as const,
  chats: (a: string) => ['chats', a] as const,
  chat: (a: string, c: string) => ['chat', a, c] as const,
  messages: (a: string, c: string) => ['messages', a, c] as const,
  search: (a: string) => ['search', a] as const,
  contacts: (a: string) => ['contacts', a] as const,
  requests: (a: string) => ['requests', a] as const,
  webhooks: ['webhooks'] as const,
  persons: ['persons'] as const,
  person: (p: string) => ['person', p] as const,
  personMessages: (p: string) => ['person-messages', p] as const,
  suggestions: ['person-suggestions'] as const,
}

// --- meta ---

export function useStatus() {
  return useQuery({ queryKey: qk.status, queryFn: () => api<BridgeStatus>('/status'), refetchInterval: 30_000 })
}

export function usePlatforms() {
  return useQuery({ queryKey: qk.platforms, queryFn: async () => (await api<{ platforms: Platform[] }>('/platforms')).platforms })
}

// --- accounts ---

export function useAccounts() {
  return useQuery({ queryKey: qk.accounts, queryFn: async () => (await api<{ accounts: Account[] }>('/accounts')).accounts })
}

export function useAccount(a: string) {
  return useQuery({ queryKey: qk.account(a), queryFn: () => api<Account>(acc(a)) })
}

function useInvalidateAccounts() {
  const qc = useQueryClient()
  return (a?: string) => {
    void qc.invalidateQueries({ queryKey: qk.accounts })
    void qc.invalidateQueries({ queryKey: qk.status })
    if (a)
      void qc.invalidateQueries({ queryKey: qk.account(a) })
  }
}

export interface CreateAccountInput {
  id: string
  platform: string
  adapter?: string
  config?: Record<string, unknown>
}

export function useCreateAccount() {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (input: CreateAccountInput) => api<Account>('/accounts', { method: 'POST', body: input }),
    onSuccess: a => invalidate(a.id),
  })
}

export function useDeleteAccount() {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (a: string) => api<void>(acc(a), { method: 'DELETE' }),
    onSuccess: () => invalidate(),
  })
}

export function useAccountAction(a: string) {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (action: 'logout' | 'reconnect') => api<Account>(`${acc(a)}/${action}`, { method: 'POST' }),
    onSuccess: () => invalidate(a),
  })
}

export function useUpdateSelf(a: string) {
  const invalidate = useInvalidateAccounts()
  return useMutation({
    mutationFn: (body: { name?: string, bio?: string, avatar_media_id?: string }) => api<Account>(`${acc(a)}/self`, { method: 'PATCH', body }),
    onSuccess: () => invalidate(a),
  })
}

// --- login ---

export function useLoginStep(a: string, enabled: boolean) {
  return useQuery({
    queryKey: qk.login(a),
    queryFn: () => api<LoginStep>(`${acc(a)}/login`),
    enabled,
    // QR codes rotate; the stream also pushes account.login_step.
    refetchInterval: q => (q.state.data?.step === 'display' ? 5_000 : false),
  })
}

export function useLogin(a: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAccounts()
  const onStep = (step: LoginStep | undefined) => {
    qc.setQueryData(qk.login(a), step)
    if (step?.step === 'done')
      invalidate(a)
  }
  return {
    start: useMutation({
      mutationFn: (flow: string) => api<LoginStep>(`${acc(a)}/login`, { method: 'POST', body: { flow } }),
      onSuccess: onStep,
    }),
    submit: useMutation({
      mutationFn: (fields: Record<string, string>) => api<LoginStep>(`${acc(a)}/login/submit`, { method: 'POST', body: { fields } }),
      onSuccess: onStep,
    }),
    cancel: useMutation({
      mutationFn: () => api<void>(`${acc(a)}/login`, { method: 'DELETE' }),
      onSuccess: () => {
        qc.removeQueries({ queryKey: qk.login(a) })
        invalidate(a)
      },
    }),
  }
}

// --- chats ---

export function useChats(a: string, archived: boolean) {
  return useInfiniteQuery({
    queryKey: [...qk.chats(a), { archived }],
    queryFn: ({ pageParam }) => api<Page<'chats', Chat>>(`${acc(a)}/chats`, { query: { archived, limit: 100, cursor: pageParam } }),
    initialPageParam: '',
    getNextPageParam: last => last.next_cursor || undefined,
  })
}

export function useChat(a: string, c: string) {
  return useQuery({ queryKey: qk.chat(a, c), queryFn: () => api<Chat>(`${acc(a)}/chats/${seg(c)}`) })
}

export function useCreateChat(a: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string, members: string[] }) => api<Chat>(`${acc(a)}/chats`, { method: 'POST', body: { kind: 'group', ...body } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.chats(a) }),
  })
}

export function usePatchChat(a: string, c: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { muted?: boolean, archived?: boolean, name?: string, tags?: string[] }) =>
      api<Chat>(`${acc(a)}/chats/${seg(c)}`, { method: 'PATCH', body }),
    onSuccess: (chat) => {
      qc.setQueryData(qk.chat(a, c), (old: Chat | undefined) => ({ ...old, ...chat, participants: old?.participants ?? chat.participants }))
      void qc.invalidateQueries({ queryKey: qk.chats(a) })
    },
  })
}

export function useResolveChat(a: string) {
  return useMutation({
    mutationFn: (handle: string) => api<ResolvedChat>(`${acc(a)}/chats/resolve`, { method: 'POST', body: { handle } }),
  })
}

export function useMarkRead(a: string, c: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>(`${acc(a)}/chats/${seg(c)}/read`, { method: 'POST', body: {} }),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.chats(a) }),
  })
}

// --- messages ---

export function useMessages(a: string, c: string, backfill: boolean) {
  return useInfiniteQuery({
    queryKey: qk.messages(a, c),
    queryFn: ({ pageParam }) =>
      api<Page<'messages', Message>>(`${acc(a)}/chats/${seg(c)}/messages`, { query: { limit: 50, cursor: pageParam, backfill: backfill ? 1 : undefined } }),
    initialPageParam: '',
    getNextPageParam: last => last.next_cursor || undefined,
  })
}

export function useSearchMessages(a: string, q: string, chat?: string) {
  return useQuery({
    queryKey: [...qk.search(a), q, chat],
    queryFn: () => api<Page<'messages', Message>>(`${acc(a)}/messages/search`, { query: { q, chat, limit: 50 } }),
    enabled: q.trim().length > 0,
    placeholderData: keepPreviousData,
  })
}

export function useUpload(a: string) {
  return useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData()
      fd.set('file', file, file.name)
      return api<Attachment>(`${acc(a)}/media`, { method: 'POST', body: fd })
    },
  })
}

export function useSendMessage(a: string, c: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: SendRequest) => api<Message>(`${acc(a)}/chats/${seg(c)}/messages`, { method: 'POST', body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.messages(a, c) })
      void qc.invalidateQueries({ queryKey: qk.chats(a) })
    },
  })
}

export function useMessageActions(a: string, c: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: qk.messages(a, c) })
  return {
    remove: useMutation({ mutationFn: (m: string) => api<Message>(`${acc(a)}/messages/${seg(m)}`, { method: 'DELETE' }), onSuccess: refresh }),
    react: useMutation({
      mutationFn: ({ message, emoji, remove }: { message: string, emoji: string, remove: boolean }) =>
        api<void>(`${acc(a)}/messages/${seg(message)}/reactions/${seg(emoji)}`, { method: remove ? 'DELETE' : 'PUT' }),
      onSuccess: refresh,
    }),
  }
}

// --- contacts ---

export function useContacts(a: string, q: string) {
  return useInfiniteQuery({
    queryKey: [...qk.contacts(a), q],
    queryFn: ({ pageParam }) => api<Page<'contacts', Contact>>(`${acc(a)}/contacts`, { query: { q, limit: 100, cursor: pageParam } }),
    initialPageParam: '',
    getNextPageParam: last => last.next_cursor || undefined,
    placeholderData: keepPreviousData,
  })
}

export function usePatchContact(a: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ user, ...body }: { user: string, alias?: string, blocked?: boolean }) =>
      api<Contact>(`${acc(a)}/contacts/${seg(user)}`, { method: 'PATCH', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.contacts(a) }),
  })
}

// --- requests ---

export function useRequests(a: string, state?: RequestState) {
  return useQuery({
    queryKey: [...qk.requests(a), state ?? 'all'],
    queryFn: async () => (await api<Page<'requests', ChatRequest>>(`${acc(a)}/requests`, { query: { state, limit: 200 } })).requests,
  })
}

/** Requests across all accounts, newest first. */
export function useAllRequests(accounts: string[], state?: RequestState) {
  return useQueries({
    queries: accounts.map(a => ({
      queryKey: [...qk.requests(a), state ?? 'all'],
      queryFn: async () => (await api<Page<'requests', ChatRequest>>(`${acc(a)}/requests`, { query: { state, limit: 200 } })).requests,
    })),
    combine: results => ({
      data: results.flatMap(r => r.data ?? []).sort((x, y) => y.created_at.localeCompare(x.created_at)),
      isLoading: results.some(r => r.isLoading),
      error: results.find(r => r.error)?.error ?? null,
    }),
  })
}

export function useAnswerRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ account, id, action, reason }: { account: string, id: string, action: RequestAction, reason?: string }) =>
      api<ChatRequest>(`${acc(account)}/requests/${seg(id)}/${action}`, { method: 'POST', body: reason ? { reason } : undefined }),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: qk.requests(r.account_id) })
      void qc.invalidateQueries({ queryKey: qk.account(r.account_id) })
      void qc.invalidateQueries({ queryKey: qk.accounts })
    },
  })
}

// --- webhooks ---

export function useWebhooks() {
  return useQuery({ queryKey: qk.webhooks, queryFn: async () => (await api<{ webhooks: Webhook[] }>('/webhooks')).webhooks ?? [] })
}

export function useWebhookActions() {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: qk.webhooks })
  return {
    create: useMutation({
      mutationFn: (body: { url: string, secret: string, account?: string, types?: string[] }) => api<Webhook>('/webhooks', { method: 'POST', body }),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => api<void>(`/webhooks/${seg(id)}`, { method: 'DELETE' }), onSuccess: refresh }),
  }
}

// --- live events → cache ---

interface WithChat {
  chat_id?: string
  id?: string
}

/** Which cached queries an event makes stale. Pure, so it is unit tested. */
export function invalidationsFor(e: ApiEvent): (readonly unknown[])[] {
  const a = e.account_id ?? ''
  const d = (e.data ?? {}) as WithChat
  switch (e.type) {
    case 'message.new':
    case 'message.updated':
    case 'message.deleted':
    case 'message.reaction':
    case 'message.receipt':
      return [qk.messages(a, d.chat_id ?? ''), qk.chats(a)]
    case 'chat.new':
    case 'chat.updated':
      return [qk.chats(a), qk.chat(a, d.id ?? '')]
    case 'contact.updated':
      return [qk.contacts(a)]
    case 'account.status':
      return [qk.accounts, qk.account(a), qk.status]
    case 'person.updated':
      return [qk.persons, qk.person(d.id ?? ''), qk.personMessages(d.id ?? ''), qk.suggestions]
    case 'request.new':
    case 'request.updated':
      return [qk.requests(a), qk.account(a), qk.accounts]
    default:
      return []
  }
}

export function useFetchMedia(a: string, c: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (mediaId: string) => api<Attachment>(`/media/${seg(mediaId)}/fetch`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.messages(a, c) }),
  })
}

// --- persons ---

export interface LinkInput {
  account_id: string
  user_id: string
}

export function usePersons(q: string, tag?: string) {
  return useQuery({
    queryKey: [...qk.persons, q, tag ?? ''],
    queryFn: async () => (await api<Page<'persons', Person>>('/persons', { query: { q, tag, limit: 200 } })).persons,
    placeholderData: keepPreviousData,
  })
}

export function usePerson(id: string) {
  return useQuery({ queryKey: qk.person(id), queryFn: () => api<Person>(`/persons/${seg(id)}`) })
}

export function usePersonSuggestions() {
  return useQuery({ queryKey: qk.suggestions, queryFn: async () => (await api<{ suggestions: PersonSuggestion[] }>('/persons/suggest')).suggestions })
}

export function usePersonMessages(id: string, scope: 'direct' | 'all') {
  return useInfiniteQuery({
    queryKey: [...qk.personMessages(id), scope],
    queryFn: ({ pageParam }) => api<Page<'messages', Message>>(`/persons/${seg(id)}/messages`, { query: { scope, limit: 50, cursor: pageParam } }),
    initialPageParam: '',
    getNextPageParam: last => last.next_cursor || undefined,
  })
}

export function usePersonActions() {
  const qc = useQueryClient()
  const refresh = (id?: string) => {
    for (const key of [qk.persons, qk.suggestions, ['contacts'], ['chats'], ['chat'], ['messages']])
      void qc.invalidateQueries({ queryKey: key })
    if (id) {
      void qc.invalidateQueries({ queryKey: qk.person(id) })
      void qc.invalidateQueries({ queryKey: qk.personMessages(id) })
    }
  }
  return {
    create: useMutation({
      mutationFn: (body: { name: string, tags?: string[], notes?: string, links?: LinkInput[] }) => api<Person>('/persons', { method: 'POST', body }),
      onSuccess: p => refresh(p.id),
    }),
    patch: useMutation({
      mutationFn: ({ id, ...body }: { id: string, name?: string, tags?: string[], notes?: string }) => api<Person>(`/persons/${seg(id)}`, { method: 'PATCH', body }),
      onSuccess: p => refresh(p.id),
    }),
    remove: useMutation({
      mutationFn: (id: string) => api<void>(`/persons/${seg(id)}`, { method: 'DELETE' }),
      onSuccess: () => refresh(),
    }),
    link: useMutation({
      mutationFn: ({ id, ...body }: LinkInput & { id: string }) => api<Person>(`/persons/${seg(id)}/links`, { method: 'POST', body }),
      onSuccess: p => refresh(p.id),
    }),
    unlink: useMutation({
      mutationFn: ({ id, account_id, user_id }: LinkInput & { id: string }) =>
        api<Person>(`/persons/${seg(id)}/links/${seg(account_id)}/${seg(user_id)}`, { method: 'DELETE' }),
      onSuccess: p => refresh(p.id),
    }),
    merge: useMutation({
      mutationFn: ({ id, from }: { id: string, from: string[] }) => api<Person>(`/persons/${seg(id)}/merge`, { method: 'POST', body: { from } }),
      onSuccess: p => refresh(p.id),
    }),
  }
}
