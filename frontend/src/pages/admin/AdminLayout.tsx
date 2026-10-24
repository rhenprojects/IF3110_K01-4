import { NavLink, Outlet } from "react-router-dom";

const tab = ({ isActive }: { isActive: boolean }) =>
  `border-b-2 px-4 py-2 text-sm ${isActive ? "border-slate-900 font-medium" : "border-transparent text-slate-500 hover:text-slate-900"}`;

export default function AdminLayout() {
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Platform Admin</h1>
      <nav className="mb-6 flex border-b">
        <NavLink to="users" className={tab}>Users</NavLink>
        <NavLink to="groups" className={tab}>Groups</NavLink>
        <NavLink to="connections" className={tab}>Koneksi Database</NavLink>
      </nav>
      <Outlet />
    </div>
  );
}
