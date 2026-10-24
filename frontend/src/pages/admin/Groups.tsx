import { useEffect, useState, type FormEvent } from "react";
import {
  useConnections, useCreateGroup, useDeleteGroup, useGroupAccess, useGroups, useSetAccess, useSetMembers, useUpdateGroup, useUsers,
} from "../../features/admin/hooks";
import { Button, ErrorBox, Field, Modal, PageHeader, generalError, fieldError, inputCls, td, th } from "../../components/ui";
import type { AccessEntry, AccessLevel, Group } from "../../lib/types";

function GroupForm({ group, onClose }: { group?: Group; onClose: () => void }) {
  const create = useCreateGroup();
  const update = useUpdateGroup();
  const m = group ? update : create;
  const [name, setName] = useState(group?.name ?? "");
  const [description, setDescription] = useState(group?.description ?? "");

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (group) await update.mutateAsync({ id: group.id, name, description });
      else await create.mutateAsync({ name, description });
      onClose();
    } catch {
      //[error] ditampilkan lewat m.error
    }
  }

  return (
    <Modal title={group ? `Edit ${group.name}` : "Tambah group"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <ErrorBox message={generalError(m.error, ["name"])} />
        <Field label="Nama" error={fieldError(m.error, "name")}>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Deskripsi">
          <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <div className="flex justify-end gap-2 pt-2">
          <Button onClick={onClose}>Batal</Button>
          <Button type="submit" variant="primary" disabled={m.isPending}>Simpan</Button>
        </div>
      </form>
    </Modal>
  );
}

type Level = AccessLevel | "";

//[akses] anggota + level akses per database untuk satu group
function ManageGroup({ group, onClose }: { group: Group; onClose: () => void }) {
  const users = useUsers();
  const conns = useConnections();
  const access = useGroupAccess(group.id);
  const setMembers = useSetMembers();
  const setAccess = useSetAccess();
  const [members, setMembersState] = useState<Set<string>>(new Set(group.member_ids));
  const [levels, setLevels] = useState<Record<string, Level>>({});
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (access.data) setLevels(Object.fromEntries(access.data.map((a) => [a.connection_id, a.level])));
  }, [access.data]);

  const toggle = (id: string) => {
    const n = new Set(members);
    if (n.has(id)) n.delete(id); else n.add(id);
    setMembersState(n);
  };

  async function save() {
    setError(null);
    try {
      const entries: AccessEntry[] = Object.entries(levels)
        .filter(([, l]) => l !== "")
        .map(([connection_id, level]) => ({ connection_id, level: level as AccessLevel }));
      await setMembers.mutateAsync({ id: group.id, user_ids: [...members] });
      await setAccess.mutateAsync({ id: group.id, access: entries });
      onClose();
    } catch (e) {
      setError(e);
    }
  }

  const loading = users.isLoading || conns.isLoading || access.isLoading;
  return (
    <Modal title={`Anggota & akses: ${group.name}`} onClose={onClose}>
      <ErrorBox message={generalError(error, [])} />
      {loading && <p className="text-slate-500">Memuat…</p>}
      {!loading && (
        <div className="space-y-5">
          <section>
            <h3 className="mb-2 text-sm font-medium">Anggota</h3>
            <ul className="max-h-40 space-y-1 overflow-y-auto rounded border p-2">
              {users.data?.map((u) => (
                <li key={u.id}>
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={members.has(u.id)} onChange={() => toggle(u.id)} />
                    {u.name} <span className="text-slate-400">({u.username})</span>
                  </label>
                </li>
              ))}
            </ul>
          </section>
          <section>
            <h3 className="mb-2 text-sm font-medium">Akses database</h3>
            {conns.data?.length === 0 && <p className="text-sm text-slate-500">Belum ada koneksi database.</p>}
            <ul className="space-y-2">
              {conns.data?.map((c) => (
                <li key={c.id} className="flex items-center justify-between text-sm">
                  <span>{c.name}</span>
                  <select aria-label={`Akses ${c.name}`} className="rounded border border-slate-300 px-2 py-1"
                    value={levels[c.id] ?? ""} onChange={(e) => setLevels({ ...levels, [c.id]: e.target.value as Level })}>
                    <option value="">Tidak ada akses</option>
                    <option value="view">View Only</option>
                    <option value="edit">View + Edit</option>
                  </select>
                </li>
              ))}
            </ul>
          </section>
        </div>
      )}
      <div className="flex justify-end gap-2 pt-5">
        <Button onClick={onClose}>Batal</Button>
        <Button variant="primary" onClick={save} disabled={loading || setMembers.isPending || setAccess.isPending}>Simpan</Button>
      </div>
    </Modal>
  );
}

export default function Groups() {
  const groups = useGroups();
  const del = useDeleteGroup();
  const [form, setForm] = useState<Group | "new" | null>(null);
  const [manage, setManage] = useState<Group | null>(null);

  function remove(g: Group) {
    if (window.confirm(`Hapus group "${g.name}"?`)) del.mutate(g.id);
  }

  return (
    <div>
      <PageHeader title="Groups" action={<Button variant="primary" onClick={() => setForm("new")}>Tambah group</Button>} />
      <ErrorBox message={generalError(del.error, [])} />
      {groups.isLoading && <p className="text-slate-500">Memuat…</p>}
      {groups.error && <ErrorBox message={(groups.error as Error).message} />}
      {groups.data?.length === 0 && <p className="text-slate-500">Belum ada group.</p>}
      {groups.data && groups.data.length > 0 && (
        <table className="w-full overflow-hidden rounded border bg-white">
          <thead className="bg-slate-50"><tr><th className={th}>Nama</th><th className={th}>Deskripsi</th><th className={th}>Anggota</th><th className={th} /></tr></thead>
          <tbody className="divide-y">
            {groups.data.map((g) => (
              <tr key={g.id}>
                <td className={td}>{g.name}</td>
                <td className={`${td} text-slate-500`}>{g.description || "-"}</td>
                <td className={td}>{g.member_ids.length}</td>
                <td className={`${td} text-right`}>
                  <Button onClick={() => setManage(g)}>Anggota & akses</Button>{" "}
                  <Button onClick={() => setForm(g)}>Edit</Button>{" "}
                  <Button variant="danger" onClick={() => remove(g)}>Hapus</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {form && <GroupForm group={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
      {manage && <ManageGroup group={manage} onClose={() => setManage(null)} />}
    </div>
  );
}
