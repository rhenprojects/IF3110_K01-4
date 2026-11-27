import { Link, NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useDatabase } from "../../features/databases/hooks";
import { Banner, bannerTab as tab } from "../../components/Banner";
import { DatabaseIcon } from "../../components/icons";
import { Loading } from "../../components/ui";
import { LevelBadge, StateError } from "./shared";

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
      <p className="mb-1 text-sm text-slate-500"><Link to="/databases" className="hover:underline">Databases</Link> /</p>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="min-w-0 break-words text-xl font-semibold">{db.name}</h1>
        <span className="text-sm text-slate-500">{db.db_name}</span>
        <LevelBadge level={db.level} />
      </div>
      <nav className="mb-6 flex overflow-x-auto border-b">
        <NavLink to="." end className={tab}>Schema & Data</NavLink>
        <NavLink to="migrations" end className={tab}>Migrations</NavLink>
        <NavLink to="history" className={tab}>Riwayat eksekusi</NavLink>
        <NavLink to="queue" className={tab}>Queue</NavLink>
      </nav>
      <Outlet context={db} />
    </div>
  );
}
