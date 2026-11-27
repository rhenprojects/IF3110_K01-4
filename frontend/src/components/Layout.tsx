import { Link, NavLink, Outlet, Navigate, useLocation } from "react-router-dom";
import { useAuth } from "../features/auth/auth";
import { Button } from "./ui";

//[guard] redirect ke /login jika belum login
export function RequireAuth() {
  const { user, loading, loggedOut } = useAuth();
  const loc = useLocation();
  if (loading) return <p className="p-8 text-slate-500">Memuat…</p>;
  //[guard] from = path + query agar pilihan table/tab tidak hilang; kosong setelah logout sendiri
  if (!user) return <Navigate to="/login" state={loggedOut ? null : { from: loc.pathname + loc.search }} replace />;
  return <Outlet />;
}

//[guard] halaman admin, hanya UX (backend tetap cek)
export function RequireAdmin() {
  const { user } = useAuth();
  if (!user?.is_admin) return <Navigate to="/forbidden" replace />;
  return <Outlet />;
}

const link = ({ isActive }: { isActive: boolean }) =>
  `rounded-full px-4 py-2 text-sm font-medium transition-colors sm:py-1.5 ${isActive ? "bg-primary-soft text-primary" : "text-slate-600 hover:bg-slate-100"}`;

export function AppLayout() {
  const { user, logout } = useAuth();
  return (
    <div className="flex min-h-screen flex-col bg-slate-50 text-slate-900">
      <header className="sticky top-0 z-10 flex flex-wrap items-center gap-x-6 gap-y-2 border-b bg-white/90 px-4 py-3 backdrop-blur sm:px-8">
        <Link to="/" aria-label="Migra"><img src="/logo-full.png" alt="Migra" className="h-9 w-auto" /></Link>
        <nav className="flex flex-wrap gap-1">
          <NavLink to="/databases" className={link}>Database</NavLink>
          <NavLink to="/reviews" className={link}>Review</NavLink>
          {user?.is_admin && <NavLink to="/admin" className={link}>Admin</NavLink>}
        </nav>
        <div className="ml-auto flex min-w-0 items-center gap-3 text-sm">
          <span aria-hidden className="grid size-8 shrink-0 place-items-center rounded-full bg-primary-soft font-serif font-semibold text-primary">
            {user?.name.charAt(0).toUpperCase()}
          </span>
          <span className="max-w-28 truncate font-medium sm:max-w-none">{user?.name}</span>
          <Button onClick={() => logout()}>Keluar</Button>
        </div>
      </header>
      <main className="mx-auto w-full min-w-0 max-w-6xl flex-1 p-4 sm:px-8 sm:py-8"><Outlet /></main>
      <footer className="mt-8 bg-gradient-to-r from-primary-deep via-primary to-primary-light text-white">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-4 px-4 py-6 text-sm sm:px-8">
          <div>
            <p className="font-serif text-lg font-semibold">Migra</p>
            <p className="text-white/80">Kelola migration database dengan review dan jejak yang jelas.</p>
          </div>
          <nav className="flex gap-5 text-white/90">
            <Link to="/databases" className="hover:underline">Database</Link>
            <Link to="/reviews" className="hover:underline">Review</Link>
            {user?.is_admin && <Link to="/admin" className="hover:underline">Admin</Link>}
          </nav>
          <p className="w-full border-t border-white/20 pt-4 text-xs text-white/70">© 2026 Migra · IF3110 Pengembangan Aplikasi Web</p>
        </div>
      </footer>
    </div>
  );
}
