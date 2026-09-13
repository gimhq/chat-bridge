import type { Attachment, ContentType } from '@/shared/api/types'
import { DownloadIcon, FileIcon } from 'lucide-react'
import { toast } from 'sonner'
import { useFetchMedia } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Spinner } from '@/shared/components/ui/spinner'
import { formatBytes } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { downloadMedia, useMediaUrl } from '@/shared/lib/media'

export function AttachmentView({ accountId, chatId, type, attachment: att }: { accountId: string, chatId: string, type: ContentType, attachment: Attachment }) {
  const ready = att.state === 'ready'
  const inline = ready && ['image', 'sticker', 'video', 'audio', 'voice'].includes(type)
  const media = useMediaUrl(att.media_id, inline)
  const fetchMedia = useFetchMedia(accountId, chatId)
  const name = att.file_name || `${type}-${att.media_id.slice(0, 8)}`

  if (!ready) {
    return (
      <div className="flex items-center gap-2 text-xs">
        <FileIcon className="size-4" aria-hidden />
        <span className="truncate">{name}</span>
        {att.size ? <span className="opacity-70">{formatBytes(att.size)}</span> : null}
        {att.state === 'pending' || fetchMedia.isPending
          ? <Spinner className="size-3" />
          : (
              <Button
                variant="secondary"
                size="xs"
                onClick={() => fetchMedia.mutate(att.media_id, { onError: e => toast.error(errorMessage(e)) })}
              >
                {att.state === 'failed' ? '重试下载' : '下载'}
              </Button>
            )}
      </div>
    )
  }
  if (inline && !media.url)
    return media.error ? <p className="text-xs opacity-70">无法加载媒体</p> : <Skeleton className="h-32 w-48" />
  switch (type) {
    case 'image':
    case 'sticker':
      return (
        <a href={media.url} target="_blank" rel="noopener noreferrer">
          <img src={media.url} alt={name} className={type === 'sticker' ? 'size-32 object-contain' : 'max-h-72 max-w-full rounded-lg object-contain'} />
        </a>
      )
    case 'video':
      return <video src={media.url} controls className="max-h-72 max-w-full rounded-lg" />
    case 'audio':
    case 'voice':
      return <audio src={media.url} controls className="max-w-full" />
    default:
      return (
        <Button variant="secondary" size="sm" className="max-w-full justify-start" onClick={() => void downloadMedia(att.media_id, name).catch(e => toast.error(errorMessage(e)))}>
          <DownloadIcon />
          <span className="truncate">{name}</span>
          {att.size ? <span className="opacity-70">{formatBytes(att.size)}</span> : null}
        </Button>
      )
  }
}
