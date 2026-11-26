import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { writeFileSync, mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

// go:embed 가 frontend/dist 를 요구하므로 빌드 후 gitkeep 을 다시 만든다 (빌드 산출물은 커밋하지 않음)
const keepDist = () => ({
  name: 'keep-dist-gitkeep',
  closeBundle() {
    const dir = resolve(import.meta.dirname, 'dist')
    mkdirSync(dir, { recursive: true })
    writeFileSync(resolve(dir, 'gitkeep'), '')
  },
})

export default defineConfig({
  plugins: [react(), tailwindcss(), keepDist()],
  build: {
    target: 'chrome111', // WebView2(Chromium) 기준 — 휴대폰 화면도 안드로이드 크롬 기준
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      // index.html = PC 앱 화면, phone.html = 휴대폰 연결 화면 (PC 의 DPR 이 사내망으로 열어 줌, phone.go)
      input: { main: resolve(import.meta.dirname, 'index.html'), phone: resolve(import.meta.dirname, 'phone.html') },
    },
  },
})
