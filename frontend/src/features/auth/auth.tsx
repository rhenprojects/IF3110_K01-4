import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, setUnauthorizedHandler } from "../../lib/api";

export type { User } from "../../lib/types";
import type { User } from "../../lib/types";

type AuthCtx = {
  user: User | null;
  loading: boolean;
  sessionExpired: boolean; //[session] true jika user terlempar karena 401 di tengah pemakaian
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
};

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [sessionExpired, setSessionExpired] = useState(false);

  //[session] buang cache selain "me" agar data user sebelumnya tidak tampil ke user berikutnya
  const dropCache = useCallback(() => qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" }), [qc]);

  //[session] 401 dari endpoint mana pun = sesi habis atau dicabut; RequireAuth lalu mengarahkan ke /login
  useEffect(() => {
    setUnauthorizedHandler(() => {
      setSessionExpired(true);
      qc.setQueryData(["me"], null);
      dropCache();
    });
    return () => setUnauthorizedHandler(null);
  }, [qc, dropCache]);

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
    mutationFn: (v: { username: string; password: string }) =>
      api<{ user: User }>("/users/login", { method: "POST", body: JSON.stringify(v) }),
    onSuccess: (data) => {
      dropCache();
      setSessionExpired(false);
      qc.setQueryData(["me"], data.user);
    },
  });

  const logoutM = useMutation({
    mutationFn: () => api<void>("/users/logout", { method: "POST" }),
    onSettled: () => {
      setSessionExpired(false);
      qc.setQueryData(["me"], null);
      dropCache();
    },
  });

  return (
    <Ctx.Provider
      value={{
        user: me.data ?? null,
        loading: me.isLoading,
        sessionExpired,
        login: async (username, password) => void (await loginM.mutateAsync({ username, password })),
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
