import QRCode from 'qrcode'
import { useEffect, useState } from 'react'
import { Skeleton } from '@/shared/components/ui/skeleton'

/** Renders login QR data. The image carries its own white quiet zone so it scans in dark mode. */
export function QrCode({ data, size = 232 }: { data: string, size?: number }) {
  const [src, setSrc] = useState<{ data: string, url?: string }>()
  useEffect(() => {
    let alive = true
    QRCode.toDataURL(data, { width: size, margin: 2, errorCorrectionLevel: 'M' })
      .then((url) => {
        if (alive)
          setSrc({ data, url })
      })
      .catch(() => {
        if (alive)
          setSrc({ data })
      })
    return () => {
      alive = false
    }
  }, [data, size])
  if (src?.data !== data)
    return <Skeleton style={{ width: size, height: size }} />
  if (!src.url)
    return <code className="break-all text-xs">{data}</code>
  return <img src={src.url} width={size} height={size} alt="登录二维码" className="rounded-md border" />
}
