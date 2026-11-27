import { EmptyState } from "../components/ui";
import { BackLink } from "./databases/shared";

export default function Forbidden() {
  return <EmptyState title="403 · Akses ditolak" hint="Kamu tidak punya akses ke halaman ini." action={<BackLink to="/databases">Kembali ke daftar database</BackLink>} />;
}
