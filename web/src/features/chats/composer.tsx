import type { FormEvent, KeyboardEvent } from 'react'
import type { Message } from '@/shared/api/types'
import { PaperclipIcon, SendIcon, XIcon } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import { useSendMessage, useUpload } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Spinner } from '@/shared/components/ui/spinner'
import { Textarea } from '@/shared/components/ui/textarea'
import { clientId, contentPreview, formatBytes, senderName } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { contentTypeFor } from './chat-utils'

interface Props {
  accountId: string
  chatId: string
  disabled: boolean
  replyTo?: Message
  onClearReply: () => void
}

export function Composer({ accountId, chatId, disabled, replyTo, onClearReply }: Props) {
  const [text, setText] = useState('')
  const [file, setFile] = useState<File>()
  const pickerRef = useRef<HTMLInputElement>(null)
  const upload = useUpload(accountId)
  const send = useSendMessage(accountId, chatId)
  const busy = upload.isPending || send.isPending

  async function submit(e?: FormEvent) {
    e?.preventDefault()
    if (busy || (text.trim() === '' && !file))
      return
    try {
      const att = file ? await upload.mutateAsync(file) : undefined
      await send.mutateAsync({
        client_id: clientId(),
        reply_to: replyTo?.id,
        content: att
          ? { type: contentTypeFor(att.mime), text: text.trim() || undefined, attachments: [{ media_id: att.media_id, mime: att.mime, state: att.state }] }
          : { type: 'text', text },
      })
      setText('')
      setFile(undefined)
      if (pickerRef.current)
        pickerRef.current.value = ''
      onClearReply()
    }
    catch (err) {
      toast.error(errorMessage(err))
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void submit()
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t p-3">
      {disabled && <p className="text-xs text-muted-foreground">账号未连接，暂时无法发送。</p>}
      {replyTo && (
        <div className="flex items-center gap-2 rounded-md bg-muted px-2 py-1 text-xs">
          <span className="truncate">
            回复
            {senderName(replyTo)}
            ：
            {contentPreview(replyTo.content)}
          </span>
          <Button type="button" variant="ghost" size="icon-xs" className="ml-auto" aria-label="取消回复" onClick={onClearReply}><XIcon /></Button>
        </div>
      )}
      {file && (
        <div className="flex items-center gap-2 rounded-md bg-muted px-2 py-1 text-xs">
          <PaperclipIcon className="size-3" aria-hidden />
          <span className="truncate">{file.name}</span>
          <span className="text-muted-foreground">{formatBytes(file.size)}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="ml-auto"
            aria-label="移除附件"
            onClick={() => {
              setFile(undefined)
              if (pickerRef.current)
                pickerRef.current.value = ''
            }}
          >
            <XIcon />
          </Button>
        </div>
      )}
      <div className="flex items-end gap-2">
        <input ref={pickerRef} type="file" className="hidden" aria-hidden tabIndex={-1} onChange={e => setFile(e.target.files?.[0])} />
        <Button type="button" variant="ghost" size="icon" aria-label="添加附件" disabled={disabled || busy} onClick={() => pickerRef.current?.click()}>
          <PaperclipIcon />
        </Button>
        <Textarea
          value={text}
          rows={1}
          disabled={disabled}
          placeholder={file ? '附言（可选）' : '输入消息，Enter 发送，Shift+Enter 换行'}
          aria-label="消息内容"
          className="max-h-40 min-h-9 resize-none"
          onChange={e => setText(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <Button type="submit" size="icon" aria-label="发送" disabled={disabled || busy || (text.trim() === '' && !file)}>
          {busy ? <Spinner /> : <SendIcon />}
        </Button>
      </div>
    </form>
  )
}
