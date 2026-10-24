import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

type Kind = "success" | "error";
type Item = { id: number; kind: Kind; text: string };
type ToastApi = { success: (text: string) => void; error: (text: string) => void };

const Ctx = createContext<ToastApi | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Item[]>([]);
  const nextId = useRef(0);

  const push = useCallback((kind: Kind, text: string) => {
    const id = nextId.current++;
    setItems((l) => [...l, { id, kind, text }]);
    setTimeout(() => setItems((l) => l.filter((t) => t.id !== id)), 4000);
  }, []);

  const api = useMemo<ToastApi>(() => ({ success: (t) => push("success", t), error: (t) => push("error", t) }), [push]);

  return (
    <Ctx.Provider value={api}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed bottom-4 right-4 z-20 flex flex-col gap-2">
        {items.map((t) => (
          <div key={t.id}
            className={`rounded px-4 py-2 text-sm shadow-lg ${t.kind === "success" ? "bg-slate-900 text-white" : "bg-red-600 text-white"}`}>
            {t.text}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

export function useToast() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useToast must be used inside ToastProvider");
  return v;
}
