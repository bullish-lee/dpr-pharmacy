import { convertHangulToQwerty } from 'es-hangul'
import { useEffect, useRef } from 'react'

// 키보드 방식(USB/블루투스) 2D 스캐너 입력 캡처.
//
// - 스캐너는 키보드처럼 글자를 아주 빠르게(키 간격 수 ms) 치고 Enter 로 끝낸다 → '버스트'로 인식.
// - 한글 입력 상태(IME)여도 e.code(물리 키)로 글자를 복원한다 → 제조번호의 영문이 한글 자모로 깨지지 않음.
// - GS(구분자, 0x1D)는 스캐너가 Ctrl+] 로 보내는 경우가 많다 → GS 로 변환. (스캐너 설정에서 '~'·'|' 대체도 Go 파서가 처리)
// - 사람 타이핑(느린 입력)은 무시한다.

const SHIFTED: Record<string, [string, string]> = {
  Digit0: ['0', ')'], Digit1: ['1', '!'], Digit2: ['2', '@'], Digit3: ['3', '#'], Digit4: ['4', '$'],
  Digit5: ['5', '%'], Digit6: ['6', '^'], Digit7: ['7', '&'], Digit8: ['8', '*'], Digit9: ['9', '('],
  Minus: ['-', '_'], Equal: ['=', '+'], BracketLeft: ['[', '{'], BracketRight: [']', '}'], Backslash: ['\\', '|'],
  Semicolon: [';', ':'], Quote: ["'", '"'], Comma: [',', '<'], Period: ['.', '>'], Slash: ['/', '?'], Backquote: ['`', '~'],
  Space: [' ', ' '],
  Numpad0: ['0', '0'], Numpad1: ['1', '1'], Numpad2: ['2', '2'], Numpad3: ['3', '3'], Numpad4: ['4', '4'],
  Numpad5: ['5', '5'], Numpad6: ['6', '6'], Numpad7: ['7', '7'], Numpad8: ['8', '8'], Numpad9: ['9', '9'],
  NumpadDecimal: ['.', '.'], NumpadSubtract: ['-', '-'], NumpadAdd: ['+', '+'], NumpadDivide: ['/', '/'], NumpadMultiply: ['*', '*'],
}

export function charFromEvent(e: KeyboardEvent): string | null {
  if (e.ctrlKey && e.code === 'BracketRight') return '\x1d' // GS
  if (e.ctrlKey || e.metaKey || e.altKey) return null
  const m = /^Key([A-Z])$/.exec(e.code)
  if (m) {
    const upper = e.shiftKey !== e.getModifierState?.('CapsLock')
    return upper ? m[1] : m[1].toLowerCase()
  }
  const s = SHIFTED[e.code]
  if (s) return e.shiftKey ? s[1] : s[0]
  return null
}

/** 직접 입력·붙여넣기 문자열 정리: 한글로 깨진 영문 복구, 보이지 않는 GS 유지 */
export function normalizeTyped(raw: string): string {
  const t = raw.trim()
  return /[ㄱ-ㅎㅏ-ㅣ가-힣]/.test(t) ? convertHangulToQwerty(t) : t
}

export function useWedgeScanner(onScan: (raw: string) => void, { enabled = true, minLength = 8, maxGapMs = 60 } = {}) {
  const cb = useRef(onScan)
  cb.current = onScan
  useEffect(() => {
    if (!enabled) return
    let buf = ''
    let last = 0
    let gaps: number[] = []
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null
      if (t?.closest?.('[data-scan-ignore]')) return
      const now = performance.now()
      const gap = now - last
      if (gap > 300) {
        buf = ''
        gaps = []
      }
      if (e.key === 'Enter' || e.key === 'Tab' || e.code === 'NumpadEnter') {
        const fast = gaps.length > 0 && gaps.reduce((a, b) => a + b, 0) / gaps.length < maxGapMs
        if (buf.length >= minLength && fast) {
          e.preventDefault()
          e.stopPropagation()
          const raw = buf
          buf = ''
          gaps = []
          cb.current(raw)
        }
        return
      }
      const ch = charFromEvent(e)
      if (ch == null) return
      if (buf.length > 0) gaps.push(gap)
      last = now
      buf += ch
      // 버스트 중인 글자는 입력칸·버튼으로 흘러가지 않게 막는다
      if (buf.length > 1 && gap < maxGapMs) {
        e.preventDefault()
        e.stopPropagation()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [enabled, minLength, maxGapMs])
}
