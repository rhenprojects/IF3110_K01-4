import { useState, type FormEvent } from "react";
import {
  useConnections, useCreateConnection, useDeleteConnection, useTestConnection, useUpdateConnection,
} from "../../features/admin/hooks";
import {
  Button, ConfirmDialog, EmptyState, ErrorBox, Field, Loading, Modal, PageHeader, Table, fieldError, generalError, inputCls, td,
} from "../../components/ui";
import { useToast } from "../../components/toast";
import { errorMessage } from "../../lib/errors";
import type { Connection, ConnectionInput, SSLMode } from "../../lib/types";

const FIELDS = ["name", "host", "port", "db_name", "username", "password", "ssl_mode"];
const SSL: SSLMode[] = ["disable", "allow", "prefer", "require", "verify-ca", "verify-full"];

function ConnectionForm({ conn, onClose }: { conn?: Connection; onClose: () => void }) {
  const toast = useToast();
  const create = useCreateConnection();
  const update = useUpdateConnection();
  const test = useTestConnection();
  const m = conn ? update : create;
  const [f, setF] = useState({
    name: conn?.name ?? "", host: conn?.host ?? "localhost", port: String(conn?.port ?? 5432),
    db_name: conn?.db_name ?? "", username: conn?.username ?? "", password: "", ssl_mode: (conn?.ssl_mode ?? "prefer") as SSLMode,
  });
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const input = (): ConnectionInput => ({ ...f, port: Number(f.port) || undefined, password: f.password || undefined });

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (conn) await update.mutateAsync({ id: conn.id, ...input() });
      else await create.mutateAsync(input());
      toast.success(conn ? `Koneksi "${f.name}" diperbarui` : `Koneksi "${f.name}" dibuat`);
      onClose();
    } catch {
      //[error] ditampilkan lewat m.error
    }
  }

  const err = (k: string) => fieldError(m.error, k);
  return (
    <Modal title={conn ? `Edit ${conn.name}` : "Tambah koneksi database"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <ErrorBox message={generalError(m.error, FIELDS)} />
        <Field label="Nama" error={err("name")}><input className={inputCls} value={f.name} onChange={set("name")} required /></Field>
        <div className="grid grid-cols-3 gap-3">
          <div className="col-span-2"><Field label="Host" error={err("host")}><input className={inputCls} value={f.host} onChange={set("host")} required /></Field></div>
          <Field label="Port" error={err("port")}><input type="number" className={inputCls} value={f.port} onChange={set("port")} /></Field>
        </div>
        <Field label="Nama database" error={err("db_name")}><input className={inputCls} value={f.db_name} onChange={set("db_name")} required /></Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Username" error={err("username")}><input className={inputCls} value={f.username} onChange={set("username")} required /></Field>
          <Field label="Password" error={err("password")} hint={conn ? "Kosongkan jika tidak diubah" : undefined}>
            <input type="password" className={inputCls} value={f.password} onChange={set("password")} autoComplete="new-password" required={!conn} />
          </Field>
        </div>
        <Field label="SSL mode" error={err("ssl_mode")}>
          <select className={inputCls} value={f.ssl_mode} onChange={set("ssl_mode")}>{SSL.map((s) => <option key={s}>{s}</option>)}</select>
        </Field>
        <div aria-live="polite" className="text-sm">
          {test.data && <p className="rounded bg-green-50 px-3 py-2 text-green-700">Terhubung: {test.data.server_version}</p>}
          {test.error && <p className="rounded bg-red-50 px-3 py-2 text-red-700">Gagal: {(test.error as Error).message}</p>}
        </div>
        <div className="flex justify-between pt-2">
          <Button onClick={() => test.mutate({ id: conn?.id, input: input() })} disabled={test.isPending}>
            {test.isPending ? "Menguji…" : "Test koneksi"}
          </Button>
          <div className="flex gap-2">
            <Button onClick={onClose}>Batal</Button>
            <Button type="submit" variant="primary" disabled={m.isPending}>Simpan</Button>
          </div>
        </div>
      </form>
    </Modal>
  );
}

export default function Connections() {
  const toast = useToast();
  const conns = useConnections();
  const del = useDeleteConnection();
  const [form, setForm] = useState<Connection | "new" | null>(null);
  const [toDelete, setToDelete] = useState<Connection | null>(null);

  async function confirmDelete() {
    if (!toDelete) return;
    try {
      await del.mutateAsync(toDelete.id);
      toast.success(`Koneksi "${toDelete.name}" dihapus`);
    } catch {
      //[error] ditampilkan lewat del.error
    } finally {
      setToDelete(null);
    }
  }

  const add = <Button variant="primary" onClick={() => setForm("new")}>Tambah koneksi</Button>;
  return (
    <div>
      <PageHeader title="Koneksi Database" action={add} />
      <div className="mb-3"><ErrorBox message={generalError(del.error, [])} /></div>
      {conns.isLoading && <Loading />}
      {conns.error && <ErrorBox message={errorMessage(conns.error)} />}
      {conns.data?.length === 0 && (
        <EmptyState title="Belum ada koneksi" hint="Tambahkan koneksi ke database PostgreSQL yang ingin dikelola." action={add} />
      )}
      {conns.data && conns.data.length > 0 && (
        <Table columns={["Nama", "Host", "Database", "User", "SSL", ""]}>
          {conns.data.map((c) => (
            <tr key={c.id}>
              <td className={td}>{c.name}</td>
              <td className={td}>{c.host}:{c.port}</td>
              <td className={td}>{c.db_name}</td>
              <td className={td}>{c.username}</td>
              <td className={td}>{c.ssl_mode}</td>
              <td className={`${td} whitespace-nowrap text-right`}>
                <Button onClick={() => setForm(c)}>Edit</Button>{" "}
                <Button variant="danger" onClick={() => setToDelete(c)}>Hapus</Button>
              </td>
            </tr>
          ))}
        </Table>
      )}
      {form && <ConnectionForm conn={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
      {toDelete && (
        <ConfirmDialog title="Hapus koneksi"
          message={`Hapus koneksi "${toDelete.name}"? Akses group ke database ini ikut terhapus. Riwayat migration tetap tersimpan.`}
          busy={del.isPending} onConfirm={confirmDelete} onClose={() => setToDelete(null)} />
      )}
    </div>
  );
}
