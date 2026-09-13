// Mirrors internal/model (the /v1 JSON shapes). Keep in step with docs/api.md.

export type AccountStatus = 'unpaired' | 'logging_in' | 'connecting' | 'connected' | 'disconnected' | 'error'

export interface ApiErrorBody {
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface LoginRecord {
  flow: string
  identifier?: string
  at: string
}

export interface AccountStats {
  chats: number
  messages: number
  requests_pending: number
  last_inbound_at: string | null
  last_event_id: string
}

export interface Account {
  id: string
  platform: string
  adapter: string
  status: AccountStatus
  self: Contact | null
  login?: LoginRecord
  capabilities: string[]
  config?: Record<string, unknown>
  device?: Record<string, unknown>
  stats?: AccountStats
  error: ApiErrorBody | null
  created_at: string
  connected_at: string | null
}

export interface Names {
  alias?: string
  alias_source?: string
  profile?: string
  username?: string
  first?: string
  last?: string
}

export interface AvatarRef {
  media_id: string
}

export interface Contact {
  id: string
  name: string
  names: Names
  handle?: string
  phone?: string
  email?: string
  avatar: AvatarRef | null
  bio?: string
  is_self: boolean
  is_contact: boolean
  blocked: boolean
  person_id?: string
  updated_at: string
}

export type ChatKind = 'direct' | 'group' | 'channel' | 'self'

export interface Participant {
  id: string
  name?: string
  chat_name?: string
  role: string
}

export interface Chat {
  id: string
  account_id: string
  kind: ChatKind
  name?: string
  avatar: AvatarRef | null
  unread_count: number
  last_message_at: string | null
  last_message?: Message
  muted: boolean
  archived: boolean
  tags: string[]
  pinned_message_ids: string[]
  ephemeral_ttl_s: number | null
  person_id?: string
  participants?: Participant[]
}

export interface Sender {
  id: string
  name?: string
  chat_name?: string
  person_id?: string
}

export type ContentType
  = | 'text' | 'image' | 'video' | 'audio' | 'voice' | 'file' | 'sticker' | 'location' | 'contact' | 'poll'
    | 'call' | 'payment' | 'system' | 'deleted' | 'expired' | 'unsupported'

export type MediaState = 'ready' | 'pending' | 'failed' | 'remote' | 'purged'

export interface Attachment {
  media_id: string
  mime: string
  size?: number
  file_name?: string
  width?: number
  height?: number
  duration_ms?: number
  sha256?: string
  url?: string
  thumbnail_media_id?: string
  state: MediaState
}

export interface Content {
  type: ContentType
  text?: string
  format?: 'plain' | 'markdown' | 'html'
  attachments?: Attachment[]
  location?: { lat: number, lon: number, name?: string, address?: string }
  contacts?: { name: string, phones?: string[], emails?: string[] }[]
  poll?: { question: string, options: { text: string, votes: number }[], multi: boolean, closed: boolean }
  call?: { kind: string, state: string, duration_s?: number }
  system?: { kind: string, actor?: string, targets?: string[], value?: string }
  unsupported?: { platform_type: string }
}

export interface Reaction {
  emoji: string
  sender_id: string
}

export interface Message {
  id: string
  account_id: string
  chat_id: string
  sender: Sender
  from_me: boolean
  timestamp: string
  content: Content
  reply_to?: string
  thread_id?: string
  mentions: string[]
  forwarded: boolean
  ephemeral: { expires_at: string, view_once: boolean } | null
  edited_at: string | null
  deleted_at: string | null
  reactions: Reaction[]
  status?: string
  client_id?: string
}

export interface SendRequest {
  client_id?: string
  content: Content
  reply_to?: string
}

export interface LoginField {
  name: string
  type: 'text' | 'phone' | 'password' | 'code' | 'url' | string
  label?: string
  pattern?: string
}

export interface LoginStep {
  flow: string
  step: 'input' | 'display' | 'done' | 'failed'
  input?: { fields: LoginField[] }
  display?: { type: 'qr' | 'code' | 'url' | 'text' | string, data: string, expires_at?: string }
  self?: Contact
  error?: ApiErrorBody
}

export interface LoginFlow {
  id: string
  name: string
}

export interface PlatformInstance {
  id: string
  name: string
  version?: string
  remote: boolean
  capabilities: string[]
}

export interface JsonSchemaProperty {
  'type'?: string
  'description'?: string
  'default'?: unknown
  'x-secret'?: boolean
}

export interface Platform {
  id: string
  name: string
  capabilities: string[]
  login_flows: LoginFlow[]
  config_schema?: { type?: string, required?: string[], properties?: Record<string, JsonSchemaProperty> }
  instances: PlatformInstance[]
}

export type RequestKind = 'contact_request' | 'chat_invite' | 'join_request' | 'call'
export type RequestAction = 'accept' | 'reject' | 'ignore'
export type RequestState = 'pending' | 'accepted' | 'rejected' | 'expired' | 'ignored'

export interface ChatRequest {
  id: string
  account_id: string
  kind: RequestKind
  state: RequestState
  from: Sender | null
  chat: { id: string, name?: string, kind?: string } | null
  message?: string
  call?: { kind: string }
  actions: RequestAction[]
  created_at: string
  expires_at: string | null
  answered_at: string | null
}

export interface ApiEvent<T = unknown> {
  id: string
  type: string
  account_id?: string
  timestamp: string
  data: T
}

export interface Webhook {
  id: string
  url: string
  account_id?: string
  types?: string[]
  cursor: string
  failures: number
  paused_at: string | null
  created_at: string
}

export interface BridgeStatus {
  version: string
  uptime_s: number
  accounts: { id: string, platform: string, status: AccountStatus }[]
  events_cursor: string
}

export interface ResolvedChat {
  chat_id: string
  kind: ChatKind
  user_id?: string
}

export interface PersonLink {
  account_id: string
  user_id: string
  platform?: string
  name?: string
  handle?: string
  phone?: string
  source: 'manual' | 'phone'
  linked_at: string
}

export interface PersonChannel {
  account_id: string
  user_id: string
  chat_id: string
  name?: string
  last_message_at: string | null
}

export interface Person {
  id: string
  name: string
  tags: string[]
  notes: string
  links: PersonLink[]
  channels: PersonChannel[]
  created_at: string
  updated_at: string
}

export interface PersonSuggestion {
  contacts: PersonLink[]
  reason: string
}

/** Paged list envelope: `{<key>: T[], next_cursor?}`. */
export type Page<K extends string, T> = { [P in K]: T[] } & { next_cursor?: string }
