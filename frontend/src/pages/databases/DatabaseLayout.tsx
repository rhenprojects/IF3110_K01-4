import { Link, NavLink, Outlet, useParams } from "react-router-dom";
import { useDatabase } from "../../features/databases/hooks";
import { Loading } from "../../components/ui";
import { LevelBadge, StateError } from "./shared";

const tab = ({ isActive }: { isActive: boolean }) =>
  `border-b-2 px-4 py-2 text-sm ${isActive ? "border-slate-900 font-medium" : "border-transparent text-slate-500 hover:text-slate-900"}`;

//[layout] memuat info database + level efektif; 403/404/502 ditangani di sini sekali untuk semua sub-halaman
export default function DatabaseLayout() {
  const { dbId = "" } = useParams();
  const q = useDatabase(dbId);
  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} backLink />;
  const db = q.data;
  return (
    <div>
      <p className="mb-1 text-sm text-slate-500"><Link to="/databases" className="hover:underline">Databases</Link> /</p>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">{db.name}</h1>
        <span className="text-sm text-slate-500">{db.db_name}</span>
        <LevelBadge level={db.level} />
      </div>
      <nav className="mb-6 flex border-b">
        <NavLink to="." end className={tab}>Schema & Data</NavLink>
        <NavLink to="migrations" className={tab}>Migrations</NavLink>
      </nav>
      <Outlet context={db} />
    </div>
  );
}
