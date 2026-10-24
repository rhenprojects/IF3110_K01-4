import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../features/auth/auth";
import { ApiError } from "../lib/api";
import { errorMessage } from "../lib/errors";

export default function Login() {
  const { user, login, sessionExpired } = useAuth();
  const nav = useNavigate();
  const from = (useLocation().state as { from?: string } | null)?.from ?? "/databases";
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (user) return <Navigate to={from} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(username, password);
      nav(from, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? "Username atau password salah" : errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-screen place-items-center bg-slate-50">
      <form onSubmit={submit} className="w-80 space-y-4 rounded-lg border bg-white p-6 shadow-sm">
        <h1 className="text-lg font-semibold">Masuk</h1>
        <label className="block text-sm">
          Username
          <input className="mt-1 w-full rounded border px-3 py-2" value={username}
            onChange={(e) => setUsername(e.target.value)} autoComplete="username" required />
        </label>
        <label className="block text-sm">
          Password
          <input type="password" className="mt-1 w-full rounded border px-3 py-2" value={password}
            onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
        </label>
        {sessionExpired && !error && (
          <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-800">
            Sesi kamu telah berakhir. Silakan masuk lagi.
          </p>
        )}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <button disabled={busy} className="w-full rounded bg-slate-900 py-2 text-white disabled:opacity-50">
          {busy ? "Masuk…" : "Masuk"}
        </button>
      </form>
    </div>
  );
}
