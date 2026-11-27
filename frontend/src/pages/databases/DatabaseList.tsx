import { Link } from "react-router-dom";
import { useAuth } from "../../features/auth/auth";
import { useDatabases } from "../../features/databases/hooks";
import { Banner, BannerStat } from "../../components/Banner";
import { ArrowIcon, DatabaseIcon } from "../../components/icons";
import { EmptyState, Loading } from "../../components/ui";
import { LevelBadge, StateError } from "./shared";

export default function DatabaseList() {
  const { user } = useAuth();
  const q = useDatabases();
  return (
    <div>
      <Banner title="Database" icon={<DatabaseIcon />} caption="Pilih database untuk melihat skema dan mengelola migration-nya."
        action={user?.is_admin && (
          <Link to="/admin/connections" className="inline-flex items-center gap-2 rounded-lg bg-white px-4 py-2 text-sm font-medium text-primary shadow-sm hover:bg-primary-soft">
            Koneksi database <ArrowIcon />
          </Link>
        )}>
        {q.data && q.data.length > 0 && (
          <div className="flex flex-wrap gap-2">
            <BannerStat value={q.data.length} label="database" />
            <BannerStat value={q.data.filter((d) => d.level === "edit").length} label="bisa diedit" />
          </div>
        )}
      </Banner>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState
          title="Belum ada database yang bisa kamu akses"
          hint={user?.is_admin
            ? "Platform Admin tidak otomatis punya akses ke isi database. Jadikan dirimu anggota group yang punya akses ke database tersebut."
            : "Minta Platform Admin untuk menambahkanmu ke group yang punya akses database."} />
      )}
      {q.data && q.data.length > 0 && (
        <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {q.data.map((d) => (
            <li key={d.id}>
              <Link to={`/databases/${d.id}`}
                className="group flex h-full flex-col gap-4 rounded-2xl border bg-white p-5 shadow-sm transition hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-md">
                <span className="flex items-center gap-3">
                  <span aria-hidden className="grid size-11 shrink-0 place-items-center rounded-xl bg-primary-soft text-primary"><DatabaseIcon /></span>
                  <span className="min-w-0">
                    <span className="block truncate font-serif text-lg font-semibold">{d.name}</span>
                    <span className="block truncate font-mono text-xs text-slate-500">{d.db_name}</span>
                  </span>
                </span>
                <span className="mt-auto flex items-center justify-between">
                  <LevelBadge level={d.level} />
                  <span className="flex items-center gap-1 text-sm font-medium text-primary transition-transform group-hover:translate-x-0.5">Buka <ArrowIcon /></span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
