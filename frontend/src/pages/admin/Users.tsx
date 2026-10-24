import { useState, type FormEvent } from "react";
import { useAuth } from "../../features/auth/auth";
import { useCreateUser, useDeleteUser, useUpdateUser, useUsers } from "../../features/admin/hooks";
import {
  Badge, Button, ConfirmDialog, ErrorBox, Field, Loading, Modal, PageHeader, Table, fieldError, generalError, inputCls, td,
} from "../../components/ui";
import { useToast } from "../../components/toast";
import { errorMessage } from "../../lib/errors";
import type { User } from "../../lib/types";

const FIELDS = ["username", "name", "password"];

function UserForm({ user, onClose }: { user?: User; onClose: () => void }) {
  const { user: me } = useAuth();
  const toast = useToast();
  const create = useCreateUser();
  const update = useUpdateUser();
  const m = user ? update : create;
  const isSelf = !!user && user.id === me?.id;
  const [username, setUsername] = useState(user?.username ?? "");
  const [name, setName] = useState(user?.name ?? "");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(user?.is_admin ?? false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (user) await update.mutateAsync({ id: user.id, name, is_admin: isAdmin, ...(password ? { password } : {}) });
      else await create.mutateAsync({ username, name, password, is_admin: isAdmin });
      toast.success(user ? `User "${user.username}" diperbarui` : `User "${username}" dibuat`);
      onClose();
    } catch {
      //[error] ditampilkan lewat m.error
    }
  }

  return (
    <Modal title={user ? `Edit ${user.username}` : "Tambah user"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <ErrorBox message={generalError(m.error, FIELDS)} />
        {!user && (
          <Field label="Username" error={fieldError(m.error, "username")} hint="3-50 karakter: huruf, angka, _ . -">
            <input className={inputCls} value={username} onChange={(e) => setUsername(e.target.value)} required />
          </Field>
        )}
        <Field label="Nama" error={fieldError(m.error, "name")} hint={user ? undefined : "Kosong = sama dengan username"}>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Password" error={fieldError(m.error, "password")}
          hint={user ? "Kosongkan jika tidak diubah" : "8-72 karakter"}>
          <input type="password" className={inputCls} value={password} autoComplete="new-password"
            onChange={(e) => setPassword(e.target.value)} required={!user} />
        </Field>
        <div>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={isAdmin} disabled={isSelf} onChange={(e) => setIsAdmin(e.target.checked)} /> Platform Admin
          </label>
          {isSelf && <p className="mt-1 text-xs text-slate-500">Status admin diri sendiri tidak bisa dicabut.</p>}
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <Button onClick={onClose}>Batal</Button>
          <Button type="submit" variant="primary" disabled={m.isPending}>Simpan</Button>
        </div>
      </form>
    </Modal>
  );
}

export default function Users() {
  const { user: me } = useAuth();
  const toast = useToast();
  const users = useUsers();
  const del = useDeleteUser();
  const [form, setForm] = useState<User | "new" | null>(null);
  const [toDelete, setToDelete] = useState<User | null>(null);

  async function confirmDelete() {
    if (!toDelete) return;
    try {
      await del.mutateAsync(toDelete.id);
      toast.success(`User "${toDelete.username}" dihapus`);
    } catch {
      //[error] ditampilkan lewat del.error
    } finally {
      setToDelete(null);
    }
  }

  return (
    <div>
      <PageHeader title="Users" action={<Button variant="primary" onClick={() => setForm("new")}>Tambah user</Button>} />
      <div className="mb-3"><ErrorBox message={generalError(del.error, [])} /></div>
      {users.isLoading && <Loading />}
      {users.error && <ErrorBox message={errorMessage(users.error)} />}
      {users.data && (
        <Table columns={["Username", "Nama", "Role", ""]}>
          {users.data.map((u) => (
            <tr key={u.id}>
              <td className={td}>{u.username}{u.id === me?.id && <span className="ml-2 text-xs text-slate-400">(kamu)</span>}</td>
              <td className={td}>{u.name}</td>
              <td className={td}>{u.is_admin ? <Badge tone="dark">Admin</Badge> : "User"}</td>
              <td className={`${td} text-right`}>
                <Button onClick={() => setForm(u)}>Edit</Button>{" "}
                <Button variant="danger" onClick={() => setToDelete(u)} disabled={u.id === me?.id}>Hapus</Button>
              </td>
            </tr>
          ))}
        </Table>
      )}
      {form && <UserForm user={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
      {toDelete && (
        <ConfirmDialog title="Hapus user" message={`Hapus user "${toDelete.username}"? Sesi user ini juga akan dicabut.`}
          busy={del.isPending} onConfirm={confirmDelete} onClose={() => setToDelete(null)} />
      )}
    </div>
  );
}
