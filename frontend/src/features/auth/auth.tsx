import { createContext, useContext, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";

export type User = { id: string; name: string; email: string; isAdmin?: boolean };

type AuthCtx = {
  user: User | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
};

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();

  //[auth] cek session saat load, 401 = belum login
  const me = useQuery({
    queryKey: ["me"],
    retry: false,
    staleTime: 60_000,
    queryFn: async () => {
      try {
        return await api<User>("/users/profile");
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return null;
        throw e;
      }
    },
  });

  const loginM = useMutation({
    mutationFn: (v: { email: string; password: string }) =>
      api<{ user: User }>("/users/login", { method: "POST", body: JSON.stringify(v) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });

  const logoutM = useMutation({
    mutationFn: () => api<void>("/users/logout", { method: "POST" }),
    onSettled: () => qc.setQueryData(["me"], null),
  });

  return (
    <Ctx.Provider
      value={{
        user: me.data ?? null,
        loading: me.isLoading,
        login: async (email, password) => void (await loginM.mutateAsync({ email, password })),
        logout: async () => void (await logoutM.mutateAsync()),
      }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAuth must be used inside AuthProvider");
  return v;
}
