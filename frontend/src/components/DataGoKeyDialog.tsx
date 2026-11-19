import { CheckCircle2, ExternalLink, KeyRound } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, queryClient, useSettings, type service } from '../app/api'
import { Button, Callout, Dialog, Input } from './ui'

const API_PAGE = 'https://www.data.go.kr/data/15054445/openapi.do'
const PORTAL = 'https://www.data.go.kr'
const MY_KEYS = 'https://www.data.go.kr/iim/api/selectAcountList.do'

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">{n}</span>
      <div className="min-w-0 flex-1">
        <div className="font-semibold text-gray-900">{title}</div>
        <div className="mt-1 space-y-2 text-[0.8125rem] leading-relaxed text-gray-700">{children}</div>
      </div>
    </li>
  )
}

function Open({ url, children }: { url: string; children: React.ReactNode }) {
  return (
    <Button size="sm" icon={<ExternalLink className="size-3.5" />} onClick={() => api.OpenURL(url)}>
      {children}
    </Button>
  )
}

/** 공공데이터포털 인증키 받기·넣기 — 단계 안내 + 바로 열기 + 붙여 넣고 확인 */
export function DataGoKeyDialog({ open, onClose, hasKey }: { open: boolean; onClose: () => void; hasKey?: boolean }) {
  const settings = useSettings()
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  // 확인 결과 + 그때 확인한 키 ([그래도 저장]은 입력 칸이 아니라 이 키를 저장한다)
  const [check, setCheck] = useState<(service.KeyCheck & { key: string }) | null>(null)
  // 창을 닫거나 다시 확인할 때마다 늘린다 — 늦게 도착한 확인 결과는 버린다
  const seq = useRef(0)
  const close = () => {
    seq.current++
    setKey('')
    setCheck(null)
    setBusy(false)
    onClose()
  }
  const save = async (k: string) => {
    await api.SetDataGoKey(k)
    await queryClient.invalidateQueries({ queryKey: keys.all })
  }
  /** 저장·지우기 — 실패하면 알리고 창은 그대로 둔다 */
  const saveThen = async (k: string, done: () => void) => {
    setBusy(true)
    try {
      await save(k)
      done()
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy(false)
    }
  }
  const verifyAndSave = async () => {
    const k = key.trim()
    if (!k || busy) return
    const my = ++seq.current
    setBusy(true)
    setCheck(null)
    try {
      const r = await api.VerifyDataGoKey(k)
      if (my !== seq.current) return // 창을 닫았음
      setCheck({ ...r, key: k })
      if (r.ok) {
        await save(k)
        setKey('') // 같은 키로 또 누르지 않게
        toast.success('인증키를 저장했습니다. 약제급여목록을 받는 중입니다…')
        void api
          .CheckDrugMaster(true)
          .then((m) => {
            toast.success(m.message)
            void queryClient.invalidateQueries({ queryKey: keys.all })
          })
          .catch((e) => toast.error(errorText(e)))
      }
      // 등록되지 않은 키: 잘못 복사한 키와 승인 직후(아직 퍼지기 전) 키를 서버가 구분해 주지 않는다 → 자동으로 저장하지 않고 고르게 한다
    } catch (e) {
      if (my === seq.current) toast.error(errorText(e))
    } finally {
      if (my === seq.current) setBusy(false)
    }
  }
  const savePending = (k: string) =>
    void saveThen(k, () => {
      toast.success('인증키를 저장했습니다', {
        description: settings.data?.masterAuto
          ? "등록되면 자동으로 받습니다. 그 전에 '받지 못했습니다' 알림이 한 번 뜰 수 있습니다. 1시간쯤 뒤 [지금 받기]를 누르면 바로 받습니다."
          : '자동 받기가 꺼져 있습니다. 1시간쯤 뒤 [지금 받기]를 눌러 받으세요.',
        duration: 10000,
      })
      close()
    })

  return (
    <Dialog
      open={open}
      onClose={close}
      width={680}
      title={
        <span className="flex items-center gap-2">
          <KeyRound className="size-5 text-blue-600" />
          공공데이터포털 인증키 넣기
        </span>
      }
      footer={
        <div className="flex w-full items-center justify-between gap-2">
          {hasKey ? (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() =>
                window.confirm('저장된 인증키를 지울까요? 약제급여목록을 더 이상 자동으로 받지 않습니다.') &&
                void saveThen('', () => {
                  toast.success('인증키를 지웠습니다')
                  close()
                })
              }
            >
              저장된 키 지우기
            </Button>
          ) : (
            <span />
          )}
          <Button onClick={close}>닫기</Button>
        </div>
      }
    >
      <p className="mb-4 text-sm text-gray-700">
        약제급여목록(모든 급여 약의 규격·단위·상한금액)을 매달 자동으로 받으려면 공공데이터포털의 <b>무료 인증키</b>가 필요합니다. 한 번만 하면 됩니다 (5분 정도).
      </p>
      <ol className="space-y-5">
        <Step n={1} title="공공데이터포털에 로그인 (처음이면 회원가입)">
          <p>간편인증·공동인증서·아이디 중 편한 방법으로 가입·로그인합니다.</p>
          <Open url={PORTAL}>공공데이터포털 열기</Open>
        </Step>
        <Step n={2} title="「약가기준정보조회서비스」 활용신청">
          <p>
            아래 버튼으로 <b>건강보험심사평가원_약가기준정보조회서비스</b> 페이지를 열고 <b>[활용신청]</b>을 누릅니다. 활용목적은 '기타'(예: 약국 재고·약가 관리)로 적고, 상세기능은 모두 고른 뒤 동의하고
            신청합니다. 개발계정은 <b>바로 자동 승인</b>됩니다.
          </p>
          <Open url={API_PAGE}>약가기준정보조회서비스 페이지 열기</Open>
        </Step>
        <Step n={3} title="인증키 복사">
          <p>
            마이페이지 → <b>데이터 활용 → Open API → 활용신청 현황</b>에서 이 서비스를 누르면 <b>일반 인증키</b>가 보입니다. Decoding·Encoding 어느 쪽을 복사해도 됩니다.
          </p>
          <Open url={MY_KEYS}>활용신청 현황 열기 (로그인 필요)</Open>
        </Step>
        <Step n={4} title="여기에 붙여 넣고 확인">
          <div className="flex gap-2">
            <Input
              type="password"
              value={key}
              disabled={busy}
              onChange={(e) => {
                setKey(e.target.value)
                setCheck(null)
              }}
              onKeyDown={(e) => e.key === 'Enter' && !e.nativeEvent.isComposing && void verifyAndSave()}
              placeholder={hasKey ? '새 키로 바꿀 때만 붙여 넣기' : '일반 인증키 붙여 넣기 (Ctrl+V)'}
              autoComplete="off"
            />
            <Button variant="primary" loading={busy} disabled={!key.trim()} onClick={() => void verifyAndSave()}>
              확인하고 저장
            </Button>
          </div>
          <p className="text-xs text-gray-500">키는 이 PC의 설정에만 저장되고, 기록(로그)·오류 보고에서는 가려집니다.</p>
          {check && (
            <Callout
              tone={check.ok ? 'success' : check.pending ? 'warn' : 'danger'}
              action={
                check.pending ? (
                  <Button size="sm" loading={busy} onClick={() => savePending(check.key)}>
                    그래도 저장
                  </Button>
                ) : undefined
              }
            >
              {check.ok && <CheckCircle2 className="mr-1 inline size-4" />}
              {check.message}
            </Callout>
          )}
        </Step>
      </ol>
    </Dialog>
  )
}
