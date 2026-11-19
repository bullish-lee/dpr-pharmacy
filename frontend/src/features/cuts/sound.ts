// 스캔 피드백음 (놀라지 않게 짧고 낮은 소리). 성공·중복·오류를 소리만으로도 구분할 수 있게.
let ctx: AudioContext | null = null

function tone(freq: number, ms: number, at = 0, gain = 0.08) {
  ctx ??= new AudioContext()
  const t0 = ctx.currentTime + at / 1000
  const osc = ctx.createOscillator()
  const g = ctx.createGain()
  osc.type = 'sine'
  osc.frequency.value = freq
  g.gain.setValueAtTime(gain, t0)
  g.gain.exponentialRampToValueAtTime(0.0001, t0 + ms / 1000)
  osc.connect(g).connect(ctx.destination)
  osc.start(t0)
  osc.stop(t0 + ms / 1000 + 0.02)
}

export const sounds = {
  ok: () => tone(1046, 90),
  attention: () => {
    tone(880, 90)
    tone(660, 120, 120)
  },
  duplicate: () => {
    tone(440, 80)
    tone(440, 80, 130)
  },
  error: () => tone(220, 260, 0, 0.1),
}
