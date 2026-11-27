import { useEffect, useState, type FormEvent } from "react";
import {
  useConnections, useCreateGroup, useDeleteGroup, useGroupAccess, useGroups, useSetAccess, useSetMembers, useUpdateGroup, useUsers,
} from "../../features/admin/hooks";
import { DataList, type Column } from "../../components/DataList";
import {
  Badge, Button, ConfirmDialog, EmptyState, ErrorBox, Field, Loading, Modal, PageHeader, generalError, fieldError, inputCls, selectCls,
} from "../../components/ui";
import { useToast } from "../../components/toast";
import { errorMessage } from "../../lib/errors";
import type { AccessEntry, AccessLevel, Group } from "../../lib/types";

const FIELDS = ["name", "description", "leader_id"];

function GroupForm({ group, onClose }: { group?: Group; onClose: () => void }) {
  const toast = useToast();
  const create = useCreateGroup();
  const update = useUpdateGroup();
  const users = useUsers();
  const m = group ? update : create;
  const [name, setName] = useState(group?.name ?? "");
  const [description, setDescription] = useState(group?.description ?? "");
  const [leader, setLeader] = useState(group?.leader_id ?? "");
  //[review] leader dipilih dari anggota group (plus leader saat ini), jadi hanya tersedia saat edit
  const candidates = users.data?.filter((u) => group?.member_ids.includes(u.id) || u.id === group?.leader_id) ?? [];

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (group) await update.mutateAsync({ id: group.id, name, description, ...(leader && { leader_id: leader }) });
      else await create.mutateAsync({ name, description });
      toast.success(group ? `Group "${name}" diperbarui` : `Group "${name}" dibuat`);
      onClose();
    } catch {
      //[error] ditampilkan lewat m.error
    }
  }

  return (
    <Modal title={group ? `Edit ${group.name}` : "Tambah group"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-4">
        <ErrorBox message={generalError(m.error, FIELDS)} />
        <Field label="Nama" error={fieldError(m.error, "name")}>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Deskripsi" error={fieldError(m.error, "description")} hint="Maksimal 255 karakter">
          <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        {group ? (
          <Field label="Team Leader" error={fieldError(m.error, "leader_id")}
            hint={candidates.length === 0 ? "Tambahkan anggota dulu lewat Anggota & akses." : "Wajib ada agar migration database group ini bisa diajukan."}>
            <select className={inputCls} value={leader} onChange={(e) => setLeader(e.target.value)}>
              {!leader && <option value="">Belum ada leader</option>}
              {candidates.map((u) => <option key={u.id} value={u.id}>{u.name} ({u.username})</option>)}
            </select>
          </Field>
        ) : (
          <p className="rounded-xl bg-slate-100 p-3 text-sm text-slate-600">Team Leader dipilih setelah group punya anggota.</p>
        )}
        <div className="flex flex-wrap justify-end gap-2 pt-2">
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
        <div className="space-y-6">
          <section>
            <h3 className="mb-2 font-serif text-base font-semibold">
              Anggota <span className="font-sans text-sm font-normal text-slate-500">{members.size} dipilih</span>
            </h3>
            <ul className="max-h-56 overflow-y-auto rounded-xl border p-1">
              {users.data?.map((u) => (
                <li key={u.id}>
                  <label className="flex cursor-pointer items-center gap-3 rounded-lg px-2 py-2 text-sm hover:bg-slate-100">
                    <input type="checkbox" className="size-4 shrink-0 accent-primary" checked={members.has(u.id)} onChange={() => toggle(u.id)} />
                    <span className="min-w-0 flex-1 truncate">{u.name} <span className="text-slate-500">({u.username})</span></span>
                    {u.id === group.leader_id && <Badge tone="dark">Leader</Badge>}
                  </label>
                </li>
              ))}
            </ul>
          </section>
          <section>
            <h3 className="mb-2 font-serif text-base font-semibold">Akses database</h3>
            {conns.data?.length === 0 && <p className="text-sm text-slate-500">Belum ada koneksi database.</p>}
            <ul className="space-y-2">
              {conns.data?.map((c) => (
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 rounded-xl border px-3 py-2 text-sm">
                  <span className="min-w-0 break-words font-medium">{c.name}</span>
                  <select aria-label={`Akses ${c.name}`} className={selectCls}
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
      <div className="flex flex-wrap justify-end gap-2 pt-6">
        <Button onClick={onClose}>{membersSaved ? "Tutup" : "Batal"}</Button>
        <Button variant="primary" onClick={save} disabled={loading || setMembers.isPending || setAccess.isPending}>Simpan</Button>
      </div>
    </Modal>
  );
}

export default function Groups() {
  const toast = useToast();
  const groups = useGroups();
  const users = useUsers();
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

  const leaderName = (id: string) => users.data?.find((u) => u.id === id)?.name ?? "…";
  const noLeader = groups.data?.filter((g) => !g.leader_id).length ?? 0;

  const columns: Column<Group>[] = [
    {
      header: "Nama", role: "title",
      cell: (g) => <>{g.name}{g.description && <span className="block text-sm font-normal text-slate-500">{g.description}</span>}</>,
    },
    { header: "Team Leader", cell: (g) => g.leader_id ? leaderName(g.leader_id) : <Badge tone="amber">Belum ada leader</Badge> },
    { header: "Anggota", cell: (g) => `${g.member_ids.length} orang` },
    {
      header: "", role: "actions",
      cell: (g) => (
        <>
          <Button onClick={() => setManage(g)}>Anggota & akses</Button>
          <Button onClick={() => setForm(g)}>Edit</Button>
          <Button variant="danger" onClick={() => setToDelete(g)}>Hapus</Button>
        </>
      ),
    },
  ];

  const add = <Button variant="primary" onClick={() => setForm("new")}>Tambah group</Button>;
  return (
    <div>
      <PageHeader title="Groups" action={add} />
      <p className="mb-4 max-w-3xl text-sm text-slate-500">
        Group menentukan siapa yang bisa mengakses database. Team Leader setiap group wajib menyetujui perubahan di database yang diakses group-nya.
      </p>
      {noLeader > 0 && (
        <p role="status" className="mb-4 rounded-2xl bg-warn-soft p-4 text-sm text-warn">
          {noLeader} group belum punya Team Leader. Migration di database yang diakses group tersebut belum bisa diajukan.
        </p>
      )}
      <div className="mb-3"><ErrorBox message={generalError(del.error, [])} /></div>
      {groups.isLoading && <Loading />}
      {groups.error && <ErrorBox message={errorMessage(groups.error)} />}
      {groups.data?.length === 0 && (
        <EmptyState title="Belum ada group" hint="Buat group, lalu atur anggota dan akses database-nya." action={add} />
      )}
      {groups.data && groups.data.length > 0 && (
        <DataList label="Daftar group" rows={groups.data} columns={columns} rowKey={(g) => g.id} />
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
