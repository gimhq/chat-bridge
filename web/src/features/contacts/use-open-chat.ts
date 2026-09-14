import type { Contact } from '@/shared/api/types'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { useResolveChat } from '@/shared/api/queries'
import { errorMessage } from '@/shared/lib/http'

/** Opens the direct chat with a contact, resolving it through the platform when it can. */
export function useOpenChat(accountId: string, capabilities: string[]) {
  const resolve = useResolveChat(accountId)
  const navigate = useNavigate()
  return async (c: Pick<Contact, 'id' | 'handle'>) => {
    try {
      const chatId = capabilities.includes('chat.resolve') ? (await resolve.mutateAsync(c.handle || c.id)).chat_id : c.id
      void navigate({ to: '/accounts/$accountId/chats/$chatId', params: { accountId, chatId } })
    }
    catch (err) {
      toast.error(errorMessage(err))
    }
  }
}
