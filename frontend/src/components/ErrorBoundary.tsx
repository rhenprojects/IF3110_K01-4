import { Component, type ReactNode } from "react";

type State = { error: Error | null };

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <div role="alert" className="grid min-h-screen place-items-center bg-slate-50 p-6 text-center">
          <div>
            <h1 className="text-lg font-semibold">Terjadi kesalahan pada halaman</h1>
            <p className="mt-2 text-sm text-slate-600">Muat ulang halaman. Kalau masih gagal, hubungi admin.</p>
            <button className="mt-4 rounded bg-slate-900 px-3 py-2 text-sm text-white" onClick={() => location.reload()}>
              Muat ulang
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
