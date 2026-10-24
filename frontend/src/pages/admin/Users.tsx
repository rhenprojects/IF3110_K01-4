import { useState, type FormEvent } from "react";
import { useAuth } from "../../features/auth/auth";
import { useCreateUser, useDeleteUser, useUpdateUser, useUsers } from "../../features/admin/hooks";
import { Button, ErrorBox, Field, Modal, PageHeader, fieldError, generalError, inputCls, td, th } from "../../components/ui";
import type { User } from "../../lib/types";

const FIELDS = ["username", "name", "password"];

function UserForm({ user, onClose }: { user?: User; onClose: () => void }) {
  const create = useCreateUser();
  const update = useUpdateUser();
  const m = user ? update : create;
  const [username, setUsername] = useState(user?.username ?? "");
  const [name, setName] = useState(user?.name ?? "");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(user?.is_admin ?? false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (user) await update.mutateAsync({ id: user.id, name, is_admin: isAdmin, ...(password ? { password } : {}) });
      else await create.mutateAsync({ username, name, password, is_admin: isAdmin });
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
          hint={user ? "Kosongkan jika tidak diubah" : "Minimal 8 karakter"}>
          <input type="password" className={inputCls} value={password} autoComplete="new-password"
            onChange={(e) => setPassword(e.target.value)} required={!user} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin(e.target.checked)} /> Platform Admin
        </label>
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
  const users = useUsers();
  const del = useDeleteUser();
  const [form, setForm] = useState<User | "new" | null>(null);

  function remove(u: User) {
    if (window.confirm(`Hapus user "${u.username}"?`)) del.mutate(u.id);
  }

  return (
    <div>
      <PageHeader title="Users" action={<Button variant="primary" onClick={() => setForm("new")}>Tambah user</Button>} />
      <ErrorBox message={generalError(del.error, [])} />
      {users.isLoading && <p className="text-slate-500">Memuat…</p>}
      {users.error && <ErrorBox message={(users.error as Error).message} />}
      {users.data && (
        <table className="w-full overflow-hidden rounded border bg-white">
          <thead className="bg-slate-50"><tr><th className={th}>Username</th><th className={th}>Nama</th><th className={th}>Role</th><th className={th} /></tr></thead>
          <tbody className="divide-y">
            {users.data.map((u) => (
              <tr key={u.id}>
                <td className={td}>{u.username}{u.id === me?.id && <span className="ml-2 text-xs text-slate-400">(kamu)</span>}</td>
                <td className={td}>{u.name}</td>
                <td className={td}>{u.is_admin ? <span className="rounded bg-slate-900 px-2 py-0.5 text-xs text-white">Admin</span> : "User"}</td>
                <td className={`${td} text-right`}>
                  <Button onClick={() => setForm(u)}>Edit</Button>{" "}
                  <Button variant="danger" onClick={() => remove(u)} disabled={u.id === me?.id}>Hapus</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {form && <UserForm user={form === "new" ? undefined : form} onClose={() => setForm(null)} />}
    </div>
  );
}
