import { Component, StrictMode, type ErrorInfo, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

type StartupBoundaryProps = { children: ReactNode }
type StartupBoundaryState = { error: Error | null }

function removeStartupFallback() {
  document.getElementById('kova-startup-fallback')?.remove()
}

function showStartupError(error: unknown) {
  const message = error instanceof Error ? error.message : String(error)
  window.__kovaStartupError?.(message)
}

class StartupBoundary extends Component<StartupBoundaryProps, StartupBoundaryState> {
  state: StartupBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): StartupBoundaryState {
    return { error }
  }

  componentDidMount() {
    removeStartupFallback()
  }

  componentDidCatch(error: Error, _info: ErrorInfo) {
    showStartupError(error)
  }

  render() {
    if (this.state.error) {
      return (
        <main className="startup-error" role="alert">
          <h1>KOVA không thể khởi tạo giao diện</h1>
          <p>{this.state.error.message}</p>
        </main>
      )
    }
    return this.props.children
  }
}

const rootElement = document.getElementById('root')
if (!rootElement) {
  showStartupError('Không tìm thấy vùng hiển thị KOVA (#root).')
} else {
  try {
    createRoot(rootElement).render(
      <StrictMode><StartupBoundary><App /></StartupBoundary></StrictMode>,
    )
  } catch (error) {
    showStartupError(error)
  }
}
