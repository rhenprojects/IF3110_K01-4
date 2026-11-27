import { EmptyState } from "../components/ui";
import { BackLink } from "./databases/shared";

export default function NotFound() {
  return <EmptyState title="404 · Halaman tidak ditemukan" hint="Alamat yang kamu buka tidak ada." action={<BackLink to="/databases">Kembali ke daftar database</BackLink>} />;
}
