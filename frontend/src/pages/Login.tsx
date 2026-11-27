import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../features/auth/auth";
import { ApiError } from "../lib/api";
import { errorMessage } from "../lib/errors";
import { Button, ErrorBox, Field, inputCls } from "../components/ui";

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
    <div className="grid min-h-screen place-items-center bg-slate-50 p-4">
      <div className="grid w-full max-w-4xl overflow-hidden rounded-3xl border bg-white shadow-xl md:min-h-[34rem] md:grid-cols-2">
        <div className="relative hidden overflow-hidden bg-primary md:block">
          <video src="/login-migra.mp4" autoPlay muted loop playsInline aria-hidden className="absolute inset-0 size-full object-cover" />
          <div className="absolute inset-0 bg-gradient-to-t from-primary/90 via-primary/40 to-primary/10" />
          <div className="relative flex h-full flex-col justify-end p-8 text-white">
            <p className="font-serif text-3xl font-semibold leading-snug">Ubah struktur database dengan tenang.</p>
            <p className="mt-3 text-sm text-white/85">Kelola migration database dengan review dan jejak yang jelas.</p>
          </div>
        </div>
        <form onSubmit={submit} className="flex flex-col justify-center gap-5 p-8 sm:p-10">
          <img src="/logo-full.png" alt="Migra" className="h-10 w-auto self-start" />
          <div>
            <h1 className="text-2xl font-semibold">Selamat datang kembali</h1>
            <p className="mt-1 text-sm text-slate-500">Masuk untuk mengelola migration database kamu.</p>
          </div>
          <Field label="Username">
            <input className={inputCls} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required />
          </Field>
          <Field label="Password">
            <input type="password" className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
          </Field>
          {sessionExpired && !error && (
            <p role="status" className="rounded-lg bg-warn-soft px-3 py-2 text-sm text-warn">
              Sesi kamu telah berakhir. Silakan masuk lagi.
            </p>
          )}
          <ErrorBox message={error} />
          <Button type="submit" variant="primary" disabled={busy} className="w-full py-2.5">{busy ? "Masuk…" : "Masuk"}</Button>
        </form>
      </div>
    </div>
  );
}
