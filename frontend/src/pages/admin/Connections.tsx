import { useState, type FormEvent } from "react";
import {
  useConnections, useCreateConnection, useDeleteConnection, useTestConnection, useUpdateConnection,
} from "../../features/admin/hooks";
import { Button, ErrorBox, Field, Modal, PageHeader, fieldError, generalError, inputCls, td, th } from "../../components/ui";
import type { Connection, ConnectionInput, SSLMode } from "../../lib/types";

const FIELDS = ["name", "host", "port", "db_name", "username", "password", "ssl_mode"];
const SSL: SSLMode[] = ["disable", "allow", "prefer", "require", "verify-ca", "verify-full"];

function ConnectionForm({ conn, onClose }: { conn?: Connection; onClose: () => void }) {
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
  const conns = useConnections();
  const del = useDeleteConnection();
  const [form, setForm] = useState<Connection | "new" | null>(null);

  function remove(c: Connection) {
    if (window.confirm(`Hapus koneksi "${c.name}"? Akses group ke database ini ikut terhapus.`)) del.mutate(c.id);
  }

  return (
    <div>
      <PageHeader title="Koneksi Database" action={<Button variant="primary" onClick={() => setForm("new")}>Tambah koneksi</Button>} />
      <ErrorBox message={generalError(del.error, [])} />
      {conns.isLoading && <p className="text-slate-500">Memuat…</p>}
      {conns.error && <ErrorBox message={(conns.error as Error).message} />}
      {conns.data?.length === 0 && <p className="text-slate-500">Belum ada koneksi.</p>}
      {conns.data && conns.data.length > 0 && (
        <table className="w-full overflow-hidden rounded border bg-white">
          <thead className="bg-slate-50"><tr><th className={th}>Nama</th><th className={th}>Host</th><th className={th}>Database</th><th className={th}>User</th><th className={th}>SSL</th><th className={th} /></tr></thead>
          <tbody className="divide-y">
            {conns.data.map((c) => (
              <tr key={c.id}>
                <td className={td}>{c.name}</td>
                <td className={td}>{c.host}:{c.port}</td>
                <td className={td}>{c.db_name}</td>
                <td className={td}>{c.username}</td>
                <td className={td}>{c.ssl_mode}</td>
                <td className={`${td} text-right`}>
                  <Button onClick={() => setForm(c)}>Edit</Button>{" "}
                  <Button variant="danger" onClick={() => remove(c)}>Hapus</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {form && <ConnectionForm conn={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
    </div>
  );
}
