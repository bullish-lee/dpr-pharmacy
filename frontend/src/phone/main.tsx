// 휴대폰 화면 — PC 의 DPR 이 사내망으로 열어 주는 페이지 (phone.go). Wails 기능은 쓰지 않고 HTTP 로만 PC 와 주고받는다.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../styles/index.css'
import { PhoneApp } from './PhoneApp'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <PhoneApp />
  </StrictMode>,
)
