import { Link, NavLink, Outlet, Navigate, useLocation } from "react-router-dom";
import { useAuth } from "../features/auth/auth";

//[guard] redirect ke /login jika belum login
export function RequireAuth() {
  const { user, loading } = useAuth();
  const loc = useLocation();
  if (loading) return <p className="p-8 text-slate-500">Memuat…</p>;
  if (!user) return <Navigate to="/login" state={{ from: loc.pathname }} replace />;
  return <Outlet />;
}

//[guard] halaman admin, hanya UX (backend tetap cek)
export function RequireAdmin() {
  const { user } = useAuth();
  if (!user?.is_admin) return <Navigate to="/forbidden" replace />;
  return <Outlet />;
}

const link = ({ isActive }: { isActive: boolean }) =>
  `px-3 py-1.5 rounded text-sm ${isActive ? "bg-slate-900 text-white" : "text-slate-600 hover:bg-slate-100"}`;

export function AppLayout() {
  const { user, logout } = useAuth();
  return (
    <div className="min-h-screen bg-slate-50 text-slate-900">
      <header className="flex items-center gap-4 border-b bg-white px-6 py-3">
        <Link to="/" className="font-semibold">DB Platform</Link>
        <nav className="flex gap-1">
          <NavLink to="/databases" className={link}>Databases</NavLink>
          {user?.is_admin && <NavLink to="/admin" className={link}>Admin</NavLink>}
        </nav>
        <div className="ml-auto flex items-center gap-3 text-sm">
          <span className="text-slate-500">{user?.name}</span>
          <button onClick={() => logout()} className="rounded border px-3 py-1 hover:bg-slate-100">Logout</button>
        </div>
      </header>
      <main className="mx-auto max-w-6xl p-6"><Outlet /></main>
    </div>
  );
}
