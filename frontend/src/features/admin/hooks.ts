import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type {
  AccessEntry, Connection, ConnectionInput, CreateUserInput, Group, GroupInput, TestResult, UpdateUserInput, User,
} from "../../lib/types";

const json = (b: unknown) => JSON.stringify(b);

//[query] semua mutasi admin meng-invalidate cache "admin" agar tabel segar
function useMut<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["admin"] }) });
}

export const useUsers = () =>
  useQuery({ queryKey: ["admin", "users"], queryFn: () => api<{ users: User[] }>("/admin/users").then((r) => r.users) });
export const useCreateUser = () =>
  useMut((v: CreateUserInput) => api<User>("/admin/users", { method: "POST", body: json(v) }));
export const useUpdateUser = () =>
  useMut(({ id, ...v }: UpdateUserInput & { id: string }) => api<User>(`/admin/users/${id}`, { method: "PUT", body: json(v) }));
export const useDeleteUser = () => useMut((id: string) => api<void>(`/admin/users/${id}`, { method: "DELETE" }));

export const useGroups = () =>
  useQuery({ queryKey: ["admin", "groups"], queryFn: () => api<{ groups: Group[] }>("/admin/groups").then((r) => r.groups) });
export const useCreateGroup = () =>
  useMut((v: GroupInput) => api<Group>("/admin/groups", { method: "POST", body: json(v) }));
export const useUpdateGroup = () =>
  useMut(({ id, ...v }: GroupInput & { id: string }) => api<Group>(`/admin/groups/${id}`, { method: "PUT", body: json(v) }));
export const useDeleteGroup = () => useMut((id: string) => api<void>(`/admin/groups/${id}`, { method: "DELETE" }));
export const useSetMembers = () =>
  useMut(({ id, user_ids }: { id: string; user_ids: string[] }) =>
    api<Group>(`/admin/groups/${id}/members`, { method: "PUT", body: json({ user_ids }) }));

export const useGroupAccess = (id: string) =>
  useQuery({
    queryKey: ["admin", "access", id],
    queryFn: () => api<{ access: AccessEntry[] }>(`/admin/groups/${id}/access`).then((r) => r.access),
  });
export const useSetAccess = () =>
  useMut(({ id, access }: { id: string; access: AccessEntry[] }) =>
    api<{ access: AccessEntry[] }>(`/admin/groups/${id}/access`, { method: "PUT", body: json({ access }) }));

export const useConnections = () =>
  useQuery({
    queryKey: ["admin", "connections"],
    queryFn: () => api<{ connections: Connection[] }>("/admin/connections").then((r) => r.connections),
  });
export const useCreateConnection = () =>
  useMut((v: ConnectionInput) => api<Connection>("/admin/connections", { method: "POST", body: json(v) }));
export const useUpdateConnection = () =>
  useMut(({ id, ...v }: ConnectionInput & { id: string }) =>
    api<Connection>(`/admin/connections/${id}`, { method: "PUT", body: json(v) }));
export const useDeleteConnection = () => useMut((id: string) => api<void>(`/admin/connections/${id}`, { method: "DELETE" }));

//[test] koneksi belum disimpan: kirim form; sudah disimpan & password kosong: test by id
export const useTestConnection = () =>
  useMutation({
    mutationFn: ({ id, input }: { id?: string; input: ConnectionInput }) =>
      id && !input.password
        ? api<TestResult>(`/admin/connections/${id}/test`, { method: "POST" })
        : api<TestResult>("/admin/connections/test", { method: "POST", body: json(input) }),
  });
