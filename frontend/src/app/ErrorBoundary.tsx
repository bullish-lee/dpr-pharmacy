import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button, Callout } from '../components/ui'
import { hasBackend } from './api'
import { navigate, paths } from './router'
import * as App from '../../wailsjs/go/main/App'

/** 화면 그리기 오류를 기록에 남기고, 앱 전체가 멈추지 않게 이 화면만 대신 보여준다 */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey: string }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    if (hasBackend()) void App.LogClientError('render', error.message, `${error.stack ?? ''}\n${info.componentStack ?? ''}`, window.location.hash)
  }
  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null })
  }
  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="mx-auto max-w-xl py-16">
        <Callout tone="danger">
          <div className="font-semibold">이 화면을 그리는 중 오류가 났습니다</div>
          <div className="mt-1 text-[0.8125rem]">{this.state.error.message}</div>
          <div className="mt-1 text-[0.8125rem]">기록에 남겼습니다. 설정 → 오류 보고로 보내주시면 바로 고치겠습니다.</div>
        </Callout>
        <div className="mt-4 flex gap-2">
          <Button onClick={() => this.setState({ error: null })}>다시 시도</Button>
          <Button variant="primary" onClick={() => navigate(paths.settings())}>
            오류 보고하러 가기
          </Button>
        </div>
      </div>
    )
  }
}
