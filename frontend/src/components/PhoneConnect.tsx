import { useQuery } from '@tanstack/react-query'
import { Globe, KeyRound, Smartphone } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, queryClient } from '../app/api'
import { Badge, Button, Callout, Dialog, Kbd, Spinner, Switch } from './ui'

// 휴대폰 연결 (phone.go, relay.go): 휴대폰에서 사입정보 사진·포장 바코드를 찍어 이 PC 의 DPR 로 보낸다.
//  - 기본: 같은 와이파이(약국 안)에서만
//  - 외부 중계: 모바일 데이터·약국 밖에서도 (클라우드플레어 무료 중계를 거침)

export const usePhoneStatus = () => useQuery({ queryKey: [...keys.all, 'phone-status'], queryFn: () => api.PhoneStatus(), refetchInterval: 5_000 })

/** QR 코드 그림 (bwip-js 는 필요할 때만 불러온다) */
function QR({ text }: { text: string }) {
  const [svg, setSvg] = useState('')
  useEffect(() => {
    let alive = true
    void import('bwip-js/browser').then(({ qrcode, drawingSVG }) => {
      const s = qrcode({ bcid: 'qrcode', text, scale: 5, padding: 4, backgroundcolor: 'FFFFFF' } as Parameters<typeof qrcode>[0], drawingSVG())
      if (alive) setSvg(s)
    })
    return () => {
      alive = false
    }
  }, [text])
  return <div className="barcode-surface size-[220px] shrink-0 rounded-lg border border-gray-200 bg-white p-1 [&>svg]:size-full" dangerouslySetInnerHTML={{ __html: svg }} />
}

const refresh = () => queryClient.invalidateQueries({ queryKey: [...keys.all, 'phone-status'] })

/** 설정 화면·연결 창에 함께 쓰는 본문 */
export function PhoneConnectPanel() {
  const st = usePhoneStatus()
  const [busy, setBusy] = useState(false)
  const s = st.data
  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    setBusy(true)
    try {
      await fn()
      if (ok) toast.success(ok)
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy(false)
      await refresh()
    }
  }

  if (!s) return null
  const relaySwitch = (
    <div className="rounded-lg border border-gray-200 p-3">
      <Switch
        checked={s.relay}
        disabled={busy}
        onChange={(v) => void run(() => api.SetPhoneRelay(v), v ? '모바일 데이터·약국 밖에서도 쓸 수 있게 켰습니다' : '같은 와이파이에서만 쓰도록 바꿨습니다')}
        label={
          <span className="inline-flex items-center gap-1.5">
            <Globe className="size-4 text-gray-500" /> 모바일 데이터·약국 밖에서도 쓰기 (외부 중계)
          </span>
        }
      />
      <p className="mt-2 text-xs leading-relaxed text-gray-600">
        클라우드플레어(미국 인터넷 회사)의 무료 중계를 거쳐 연결합니다. 휴대폰을 와이파이에 연결하지 않아도 되고, Windows 네트워크가 '공용'이어도 됩니다. 중계를 지나가는 것은{' '}
        <b>휴대폰 화면에 보이는 회차 날짜·약품 이름 목록, 찍은 사진, 바코드 글자</b>뿐이며 재고·수량·가격·읽은 결과는 보내지 않습니다. 전송은 암호화되지만 중계 회사는 기술적으로 내용을 볼 수 있는
        위치에 있습니다. 주소는 DPR 을 켤 때마다 바뀌므로 그날 처음 쓸 때 QR 을 다시 찍으세요.
      </p>
    </div>
  )

  if (!s.enabled || !s.running) {
    return (
      <div className="space-y-3 text-[0.8125rem] text-gray-700">
        <p>
          켜 두면 <b>휴대폰에서 사입정보 사진과 포장 바코드를 찍어 이 PC 로 바로</b> 보낼 수 있습니다. 휴대폰에 앱을 설치할 필요가 없습니다.
        </p>
        {relaySwitch}
        {s.error && <Callout tone="danger">{s.error}</Callout>}
        <Button variant="primary" icon={<Smartphone className="size-4" />} loading={busy} onClick={() => void run(() => api.SetPhoneEnabled(true), '휴대폰 연결을 켰습니다')}>
          휴대폰 연결 켜기
        </Button>
        {!s.relay && (
          <p className="text-xs text-gray-500">
            처음 켤 때 Windows 보안 창(방화벽)이 뜨면 <b>'공용 네트워크'에도 체크</b>하고 <b>허용</b>을 누르세요 (이 PC 네트워크가 '공용'인 경우).
          </p>
        )}
      </div>
    )
  }

  const url = s.urls?.[0]
  const relayWaiting = s.relay && s.relayStatus !== 'ready'
  return (
    <div className="space-y-3 text-[0.8125rem] text-gray-700">
      {relaySwitch}
      {s.error && <Callout tone="danger">{s.error}</Callout>}
      {s.relay && s.relayStatus === 'error' && <Callout tone="danger">{s.relayError}</Callout>}
      {relayWaiting && s.relayStatus !== 'error' && (
        <Spinner label={s.relayStatus === 'downloading' ? '처음 한 번 중계 프로그램을 받는 중… (약 60MB, 1~2분)' : '외부 중계에 연결하는 중… (10초쯤)'} />
      )}
      {url && (
        <div className="flex flex-wrap items-start gap-4">
          <QR text={url} />
          <ol className="min-w-[240px] flex-1 list-decimal space-y-1.5 pl-5">
            {s.relay ? (
              <li>휴대폰은 <b>모바일 데이터든 와이파이든</b> 인터넷만 되면 됩니다.</li>
            ) : (
              <li>
                휴대폰을 <b>약국 와이파이</b>에 연결합니다 (이 PC 와 같은 공유기).
              </li>
            )}
            <li>
              휴대폰 <b>카메라</b>로 왼쪽 QR 코드를 찍고, 뜨는 주소를 누릅니다.
            </li>
            {s.relay ? (
              <li>주소가 매일 바뀌므로, 그날 처음 쓸 때 QR 을 다시 찍으세요.</li>
            ) : (
              <li>
                크롬 오른쪽 위 <Kbd>⋮</Kbd> → <b>홈 화면에 추가</b>를 누르면 다음부터 아이콘으로 바로 열립니다.
              </li>
            )}
            <li>회차·약품을 고르고 ① 사입정보 사진 ② 포장 바코드를 찍으면 이 PC 로 들어옵니다.</li>
            <li>
              사입정보는 <b>3단계 '사입정보 사진 확인'</b> 화면에서 확인·저장하고, 바코드는 <b>5단계 스캔 기록</b>에 바로 들어갑니다.
            </li>
          </ol>
        </div>
      )}
      {!s.relay && (
        <Callout tone="warn">
          휴대폰에서 연결이 안 되면: ① DPR 이 켜져 있는지 ② 휴대폰이 모바일 데이터가 아니라 약국 와이파이인지 ③ Windows 방화벽에서 DPR 을 (공용 네트워크도) 허용했는지 확인하세요. 와이파이가
          손님용(게스트)이면 PC 와 연결되지 않을 수 있습니다. 모바일 데이터로 쓰려면 위의 '외부 중계'를 켜세요.
        </Callout>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone="green">켜짐{s.relay ? ' · 외부 중계' : ' · 같은 와이파이'}</Badge>
        {s.pending > 0 && <Badge tone="blue">확인 기다리는 휴대폰 사진 {s.pending}장</Badge>}
        <Button
          size="sm"
          variant="ghost"
          icon={<KeyRound className="size-3.5" />}
          onClick={() => void run(() => api.ResetPhoneToken(), '연결 비밀번호를 새로 만들었습니다. 휴대폰에서 QR 코드를 다시 찍어 주세요.')}
          title="예전 QR·홈 화면 아이콘으로는 더 이상 들어올 수 없게 합니다 (휴대폰을 바꿨거나 잃어버렸을 때)"
        >
          연결 비밀번호 새로 만들기
        </Button>
        <Button size="sm" variant="ghost" loading={busy} onClick={() => void run(() => api.SetPhoneEnabled(false), '휴대폰 연결을 껐습니다')}>
          연결 끄기
        </Button>
      </div>
    </div>
  )
}

/** 화면 위쪽의 '휴대폰으로 찍기' 버튼 → 연결 창 */
export function PhoneConnectButton() {
  const [open, setOpen] = useState(false)
  const st = usePhoneStatus()
  const pending = st.data?.pending ?? 0
  return (
    <>
      <Button icon={<Smartphone className="size-4" />} onClick={() => setOpen(true)}>
        휴대폰으로 찍기
        {st.data?.running && pending > 0 && <Badge tone="blue" className="ml-1">{pending}</Badge>}
      </Button>
      <Dialog open={open} onClose={() => setOpen(false)} title="휴대폰 연결" width={700}>
        <PhoneConnectPanel />
      </Dialog>
    </>
  )
}
