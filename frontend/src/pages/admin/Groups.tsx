import { useEffect, useState, type FormEvent } from "react";
import {
  useConnections, useCreateGroup, useDeleteGroup, useGroupAccess, useGroups, useSetAccess, useSetMembers, useUpdateGroup, useUsers,
} from "../../features/admin/hooks";
import {
  Button, ConfirmDialog, EmptyState, ErrorBox, Field, Loading, Modal, PageHeader, Table, generalError, fieldError, inputCls, td,
} from "../../components/ui";
import { useToast } from "../../components/toast";
import { errorMessage } from "../../lib/errors";
import type { AccessEntry, AccessLevel, Group } from "../../lib/types";

const FIELDS = ["name", "description"];

function GroupForm({ group, onClose }: { group?: Group; onClose: () => void }) {
  const toast = useToast();
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
      toast.success(group ? `Group "${name}" diperbarui` : `Group "${name}" dibuat`);
      onClose();
    } catch {
      //[error] ditampilkan lewat m.error
    }
  }

  return (
    <Modal title={group ? `Edit ${group.name}` : "Tambah group"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <ErrorBox message={generalError(m.error, FIELDS)} />
        <Field label="Nama" error={fieldError(m.error, "name")}>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Deskripsi" error={fieldError(m.error, "description")} hint="Maksimal 255 karakter">
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

function ManageGroup({ group, onClose }: { group: Group; onClose: () => void }) {
  const toast = useToast();
  const users = useUsers();
  const conns = useConnections();
  const access = useGroupAccess(group.id);
  const setMembers = useSetMembers();
  const setAccess = useSetAccess();
  const [members, setMembersState] = useState<Set<string>>(new Set(group.member_ids));
  const [levels, setLevels] = useState<Record<string, Level>>({});
  const [error, setError] = useState<unknown>(null);
  //[simpan] anggota dan akses disimpan lewat dua request; tandai jika yang pertama sudah berhasil
  const [membersSaved, setMembersSaved] = useState(false);

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
    setMembersSaved(false);
    let step: "anggota" | "akses" = "anggota";
    try {
      const entries: AccessEntry[] = Object.entries(levels)
        .filter(([, l]) => l !== "")
        .map(([connection_id, level]) => ({ connection_id, level: level as AccessLevel }));
      await setMembers.mutateAsync({ id: group.id, user_ids: [...members] });
      step = "akses";
      setMembersSaved(true);
      await setAccess.mutateAsync({ id: group.id, access: entries });
      toast.success(`Anggota dan akses "${group.name}" disimpan`);
      onClose();
    } catch (e) {
      setError(e);
      if (step === "anggota") setMembersSaved(false);
    }
  }

  const loading = users.isLoading || conns.isLoading || access.isLoading;
  const message = generalError(error, []);
  return (
    <Modal title={`Anggota & akses: ${group.name}`} onClose={onClose}>
      <ErrorBox message={message && (membersSaved ? `Anggota sudah tersimpan, tetapi akses gagal disimpan: ${message}` : message)} />
      {loading && <Loading />}
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
        <Button onClick={onClose}>{membersSaved ? "Tutup" : "Batal"}</Button>
        <Button variant="primary" onClick={save} disabled={loading || setMembers.isPending || setAccess.isPending}>Simpan</Button>
      </div>
    </Modal>
  );
}

export default function Groups() {
  const toast = useToast();
  const groups = useGroups();
  const del = useDeleteGroup();
  const [form, setForm] = useState<Group | "new" | null>(null);
  const [manage, setManage] = useState<Group | null>(null);
  const [toDelete, setToDelete] = useState<Group | null>(null);

  async function confirmDelete() {
    if (!toDelete) return;
    try {
      await del.mutateAsync(toDelete.id);
      toast.success(`Group "${toDelete.name}" dihapus`);
    } catch {
      //[error] ditampilkan lewat del.error
    } finally {
      setToDelete(null);
    }
  }

  const add = <Button variant="primary" onClick={() => setForm("new")}>Tambah group</Button>;
  return (
    <div>
      <PageHeader title="Groups" action={add} />
      <div className="mb-3"><ErrorBox message={generalError(del.error, [])} /></div>
      {groups.isLoading && <Loading />}
      {groups.error && <ErrorBox message={errorMessage(groups.error)} />}
      {groups.data?.length === 0 && (
        <EmptyState title="Belum ada group" hint="Buat group, lalu atur anggota dan akses database-nya." action={add} />
      )}
      {groups.data && groups.data.length > 0 && (
        <Table columns={["Nama", "Deskripsi", "Anggota", ""]}>
          {groups.data.map((g) => (
            <tr key={g.id}>
              <td className={td}>{g.name}</td>
              <td className={`${td} text-slate-500`}>{g.description || "-"}</td>
              <td className={td}>{g.member_ids.length}</td>
              <td className={`${td} whitespace-nowrap text-right`}>
                <Button onClick={() => setManage(g)}>Anggota & akses</Button>{" "}
                <Button onClick={() => setForm(g)}>Edit</Button>{" "}
                <Button variant="danger" onClick={() => setToDelete(g)}>Hapus</Button>
              </td>
            </tr>
          ))}
        </Table>
      )}
      {form && <GroupForm group={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
      {manage && <ManageGroup group={manage} onClose={() => setManage(null)} />}
      {toDelete && (
        <ConfirmDialog title="Hapus group" message={`Hapus group "${toDelete.name}"? Keanggotaan dan akses database group ini ikut terhapus.`}
          busy={del.isPending} onConfirm={confirmDelete} onClose={() => setToDelete(null)} />
      )}
    </div>
  );
}
