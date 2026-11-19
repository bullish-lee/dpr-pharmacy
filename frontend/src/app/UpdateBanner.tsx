import { Download, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { Button } from '../components/ui'
import { api, type service } from './api'

/** 켤 때 새 버전이 확인되면 화면 위쪽에 띄우는 알림 띠 */
export function UpdateBanner() {
  const [info, setInfo] = useState<service.UpdateInfo | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    try {
      return EventsOn('update-available', (i: service.UpdateInfo) => setInfo(i))
    } catch {
      return undefined
    }
  }, [])
  if (!info) return null
  return (
    <div className="flex items-center gap-3 border-b border-blue-200 bg-blue-50 px-6 py-2 text-sm text-blue-900 max-[1280px]:px-4">
      <Download className="size-4 shrink-0" />
      <span className="min-w-0 flex-1 truncate">
        새 버전 <b>v{info.latest}</b> 이 있습니다 (현재 v{info.current}). 데이터는 그대로 두고 프로그램만 바꿉니다 — 바꾸기 전에 자동 백업합니다.
      </span>
      <Button
        size="sm"
        variant="primary"
        loading={busy}
        onClick={async () => {
          if (!window.confirm(`v${info.latest} 로 업데이트하고 다시 시작할까요?`)) return
          setBusy(true)
          try {
            const r = await api.ApplyUpdate(info.latest) // 확인한 버전만 설치 (그사이 최신이 바뀌면 거부)
            toast.success(r?.message ?? '업데이트했습니다. 다시 시작합니다.')
          } catch (e) {
            toast.error(String(e))
            setBusy(false)
          }
        }}
      >
        지금 업데이트
      </Button>
      <button className="rounded p-1 text-blue-700 hover:bg-blue-100" title="나중에" onClick={() => setInfo(null)}>
        <X className="size-4" />
      </button>
    </div>
  )
}
