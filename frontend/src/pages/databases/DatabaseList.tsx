import { Link } from "react-router-dom";
import { useAuth } from "../auth/auth";
import { useDatabases } from "../../features/databases/hooks";
import { EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { LevelBadge, StateError } from "./shared";

export default function DatabaseList() {
  const { user } = useAuth();
  const q = useDatabases();
  return (
    <div>
      <PageHeader title="Databases" />
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
        <Table columns={["Nama", "Database", "Akses"]}>
          {q.data.map((d) => (
            <tr key={d.id}>
              <td className={td}>
                <Link to={`/databases/${d.id}`} className="font-medium underline-offset-2 hover:underline">{d.name}</Link>
              </td>
              <td className={`${td} text-slate-500`}>{d.db_name}</td>
              <td className={td}><LevelBadge level={d.level} /></td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}
