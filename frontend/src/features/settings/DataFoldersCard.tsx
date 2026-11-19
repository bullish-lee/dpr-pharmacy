import { useQuery, useQueryClient } from '@tanstack/react-query'
import { FolderInput, FolderOpen, HardDriveDownload } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, useAction } from '../../app/api'
import { dateTime } from '../../app/format'
import { Badge, Button, Callout, Card } from '../../components/ui'

function size(bytes: number) {
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)}GB`
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)}MB`
  return `${Math.max(1, Math.round(bytes / 1024))}KB`
}

/** 데이터·기록·백업 폴더 — 어디에 쌓이는지 보고, 열고, 옮긴다 */
export function DataFoldersCard({ version, engine }: { version?: string; engine?: string }) {
  const qc = useQueryClient()
  const info = useQuery({ queryKey: [...keys.all, 'dataDirInfo'], queryFn: () => api.DataDirInfo() })
  const d = info.data
  const [busy, setBusy] = useState(false)
  const refresh = () => qc.invalidateQueries({ queryKey: [...keys.all, 'dataDirInfo'] })
  const backup = useAction(() => api.Backup(), { success: (p) => `백업했습니다: ${p}` })
  const setBackup = useAction((dir: string) => api.SetBackupFolder(dir), { success: '백업 폴더를 바꿨습니다' })

  // 데이터 폴더 옮기기: 빈 폴더면 지금 자료를 옮기고, 자료가 있는 폴더면 그 자료로 바꾼다 (둘 다 다시 시작)
  const move = async () => {
    try {
      const dir = await api.PickFolder('데이터 폴더를 옮길 곳 — 빈 폴더(새 폴더)를 고르세요')
      if (!dir) return
      const fc = await api.CheckFolder(dir)
      if (fc.empty && fc.writable && !fc.hasData) {
        if (!window.confirm(`지금 자료를 이 폴더로 옮기고 다시 시작할까요?\n\n${fc.path}\n\n원래 폴더의 자료는 지우지 않고 그대로 둡니다. 옮기는 동안(몇 초~몇 분)은 다른 작업을 할 수 없습니다.`)) return
        setBusy(true)
        toast.success(await api.MoveDataDir(fc.path), { duration: 10000 })
      } else if (fc.hasData && !fc.newer && fc.writable) {
        const when = fc.lastWrite ? `마지막 수정 ${dateTime(fc.lastWrite)}` : ''
        const stale = d?.defaultDir && fc.path.toLowerCase() === d.defaultDir.toLowerCase() ? '\n\n주의: 기본 위치의 자료는 데이터 폴더를 옮기기 전의 옛 자료입니다 — 그 뒤에 바뀐 내용은 들어 있지 않습니다.' : ''
        if (!window.confirm(`이 폴더에 이미 급여약품 관리 자료가 있습니다 (${when}).\n\n${fc.path}${stale}\n\n이 자료로 바꾸고 다시 시작할까요? 지금 쓰는 자료는 그대로 남습니다.`)) return
        setBusy(true)
        toast.success(await api.UseDataDir(fc.path), { duration: 10000 })
      } else if (fc.hasData && !fc.writable) {
        toast.error('이 폴더에 쓸 권한이 없습니다')
      } else {
        toast.error(fc.note || '이 폴더로는 옮길 수 없습니다')
      }
    } catch (e) {
      toast.error(errorText(e), { duration: 10000 })
    } finally {
      setBusy(false)
    }
  }
  const pickBackup = async () => {
    const dir = await api.PickFolder('백업을 저장할 폴더 (USB·외장 드라이브·다른 디스크 추천)')
    if (!dir) return
    await setBackup.mutateAsync([dir]).catch(() => undefined)
    refresh()
  }

  return (
    <Card title="데이터 · 백업">
      <dl className="mb-4 grid grid-cols-[auto_1fr] items-start gap-x-4 gap-y-2.5 text-sm">
        <dt className="pt-0.5 text-gray-600">데이터 폴더</dt>
        <dd className="min-w-0">
          <div className="code break-all">{d?.dir}</div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500">
            <Badge tone={d?.source === 'custom' ? 'blue' : 'outline'}>{d?.sourceLabel}</Badge>
            {d && <span>자료(dpr.db) {size(d.dbSize)}</span>}
          </div>
          {d?.locationFile && (
            <div className="mt-1 text-xs text-gray-500">
              위치 기록: <span className="code break-all">{d.locationFile}</span> — 지우면 기본 위치({d.defaultDir})의 옮기기 전 자료로 돌아갑니다
            </div>
          )}
        </dd>
        <dt className="pt-0.5 text-gray-600">기록(로그)</dt>
        <dd className="min-w-0">
          <div className="code break-all">{d?.logDir}</div>
          <div className="mt-1 text-xs text-gray-500">앱 dpr.log · 작업 스케줄러 dpr-cli.log — 5MB마다 나눠 최근 5개 보관</div>
        </dd>
        <dt className="pt-0.5 text-gray-600">백업 폴더</dt>
        <dd className="min-w-0">
          <div className="code break-all">{d?.backupDir}</div>
          <div className="mt-1 text-xs text-gray-500">
            {d?.backupCustom ? '따로 정한 폴더' : '기본 (데이터 폴더 안)'} · 하루 1회 자동, 최근 30개 보관 · 지금 {d?.backupCount ?? 0}개 · 마지막{' '}
            {d?.lastBackup ? dateTime(d.lastBackup) : '없음'}
          </div>
          {d?.backupNote && (
            <Callout tone="warn" className="mt-2">
              {d.backupNote}
            </Callout>
          )}
        </dd>
        <dt className="text-gray-600">버전</dt>
        <dd>
          {version} <span className="ml-2 text-xs text-gray-500">{engine}</span>
        </dd>
      </dl>
      <div className="flex flex-wrap gap-2">
        <Button icon={<FolderOpen className="size-4" />} onClick={() => api.OpenDataFolder()}>
          데이터 폴더 열기
        </Button>
        <Button icon={<FolderOpen className="size-4" />} onClick={() => api.OpenLogFolder()}>
          기록 폴더 열기
        </Button>
        <Button icon={<FolderOpen className="size-4" />} onClick={() => api.OpenBackupFolder()}>
          백업 폴더 열기
        </Button>
        <Button icon={<HardDriveDownload className="size-4" />} loading={backup.isPending} onClick={() => backup.mutateAsync([]).then(refresh).catch(() => undefined)}>
          지금 백업
        </Button>
      </div>
      <div className="mt-3 flex flex-wrap gap-2 border-t border-gray-100 pt-3">
        <Button icon={<FolderInput className="size-4" />} loading={busy} disabled={!d?.canMove} title={d?.moveNote || undefined} onClick={move}>
          데이터 폴더 옮기기…
        </Button>
        <Button onClick={pickBackup} loading={setBackup.isPending}>
          백업 폴더 바꾸기…
        </Button>
        {d?.backupCustom && (
          <Button variant="ghost" onClick={() => setBackup.mutateAsync(['']).then(refresh).catch(() => undefined)}>
            백업 폴더를 기본으로
          </Button>
        )}
      </div>
      {d && !d.canMove && <p className="mt-2 text-xs text-gray-500">{d.moveNote}</p>}
      <Callout tone="info" className="mt-4">
        약국 자료는 이 PC의 데이터 폴더에만 저장되고 밖으로 보내지 않습니다. 인터넷은 복지부 고시·표준코드·약제급여목록(공공데이터포털) 받기, 새 버전 확인, (누를 때) 오류 보고에만 씁니다. 데이터 폴더는 이
        PC의 디스크나 USB에 두세요 (네트워크 폴더는 안 됨). 백업은 USB나 다른 디스크로 정해 두면 PC가 고장 나도 되살릴 수 있습니다.
      </Callout>
      {busy && (
        // 옮기는 동안 다른 수정이 옛 폴더에만 들어가지 않게 화면을 막는다
        <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/40" role="alertdialog" aria-live="assertive">
          <div className="rounded-xl bg-gray-0 px-6 py-5 text-sm shadow-xl">
            <div className="font-semibold text-gray-900">데이터 폴더를 바꾸는 중…</div>
            <div className="mt-1 text-gray-600">끝나면 프로그램이 다시 시작합니다. 잠시 기다려 주세요.</div>
          </div>
        </div>
      )}
    </Card>
  )
}
