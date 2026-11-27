import { Link, NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useDatabase } from "../../features/databases/hooks";
import { Banner, bannerTab as tab } from "../../components/Banner";
import { DatabaseIcon } from "../../components/icons";
import { Loading } from "../../components/ui";
import { StateError } from "./shared";

//[layout] memuat info database + level efektif; 403/404/502 ditangani di sini sekali untuk semua sub-halaman
export default function DatabaseLayout() {
  const { dbId = "" } = useParams();
  const { pathname } = useLocation();
  const q = useDatabase(dbId);
  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} backLink />;
  const db = q.data;
  return (
    <div>
      <Banner icon={<DatabaseIcon />} title={db.name}
        eyebrow={<Link to="/databases" className="hover:underline">← Semua database</Link>}
        meta={<>
          <span className="rounded-full bg-white/15 px-2.5 py-0.5 font-mono text-xs">{db.db_name}</span>
          <span className="rounded-full bg-white/15 px-2.5 py-0.5 text-xs font-medium">{db.level === "edit" ? "View + Edit" : "View Only"}</span>
        </>}
        caption={db.level === "edit" ? "Lihat struktur dan data, lalu ajukan migration untuk mengubahnya." : "Kamu bisa melihat struktur, data, dan riwayat perubahan database ini."}>
        <nav className="flex gap-1 overflow-x-auto">
          <NavLink to="." end className={tab}>Schema & Data</NavLink>
          <NavLink to="migrations" className={({ isActive }) => tab({ isActive: isActive && !pathname.includes("/migrations/batches") })}>Migrations</NavLink>
          <NavLink to="history" className={tab}>Riwayat eksekusi</NavLink>
          <NavLink to="queue" className={tab}>Queue</NavLink>
          <NavLink to="migrations/batches" className={tab}>Batch</NavLink>
        </nav>
      </Banner>
      <Outlet context={db} />
    </div>
  );
}
