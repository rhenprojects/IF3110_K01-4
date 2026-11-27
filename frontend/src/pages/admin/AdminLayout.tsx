import { NavLink, Outlet } from "react-router-dom";
import { Banner, bannerTab } from "../../components/Banner";
import { ShieldIcon } from "../../components/icons";

export default function AdminLayout() {
  return (
    <div>
      <Banner title="Platform Admin" icon={<ShieldIcon />} caption="Kelola pengguna, group beserta Team Leader-nya, dan koneksi ke database target.">
        <nav className="flex gap-1 overflow-x-auto">
          <NavLink to="users" className={bannerTab}>Users</NavLink>
          <NavLink to="groups" className={bannerTab}>Groups</NavLink>
          <NavLink to="connections" className={bannerTab}>Koneksi Database</NavLink>
        </nav>
      </Banner>
      <Outlet />
    </div>
  );
}
