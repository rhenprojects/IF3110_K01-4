import { Link, NavLink, Outlet, Navigate, useLocation } from "react-router-dom";
import { useAuth } from "../features/auth/auth";

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
  `rounded px-3 py-2 text-sm sm:py-1.5 ${isActive ? "bg-slate-900 text-white" : "text-slate-600 hover:bg-slate-100"}`;

export function AppLayout() {
  const { user, logout } = useAuth();
  return (
    <div className="min-h-screen bg-slate-50 text-slate-900">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b bg-white px-4 py-3 sm:px-6">
        <Link to="/" className="font-semibold">DB Platform</Link>
        <nav className="flex flex-wrap gap-1">
          <NavLink to="/databases" className={link}>Databases</NavLink>
          <NavLink to="/reviews" className={link}>Review</NavLink>
          {user?.is_admin && <NavLink to="/admin" className={link}>Admin</NavLink>}
        </nav>
        <div className="ml-auto flex min-w-0 items-center gap-3 text-sm">
          <span className="max-w-28 truncate text-slate-500 sm:max-w-none">{user?.name}</span>
          <button onClick={() => logout()} className="rounded border px-3 py-1.5 hover:bg-slate-100 sm:py-1">Logout</button>
        </div>
      </header>
      <main className="mx-auto min-w-0 max-w-6xl p-4 sm:p-6"><Outlet /></main>
    </div>
  );
}
