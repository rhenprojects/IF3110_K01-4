import { useEffect, useRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { ApiError } from "../lib/api";
import { errorMessage } from "../lib/errors";

export const inputCls = "mt-1 w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm focus:outline-2 focus:outline-primary";

export const selectCls = "rounded-lg border border-slate-300 bg-white px-2.5 py-1.5 text-sm focus:outline-2 focus:outline-primary";

//[error] pesan error field tertentu dari backend (400 + field)
export const fieldError = (e: unknown, field: string) =>
  e instanceof ApiError && e.field === field ? e.message : undefined;

//[error] pesan umum: tampil jika bukan error field yang ditangani form
export const generalError = (e: unknown, fields: string[]) => {
  if (!e) return undefined;
  if (e instanceof ApiError && e.field && fields.includes(e.field)) return undefined;
  return errorMessage(e);
};

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    const el = box.current!;
    const previous = document.activeElement as HTMLElement | null;
    const items = () => Array.from(el.querySelectorAll<HTMLElement>(FOCUSABLE));
    (items()[0] ?? el).focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") return close.current();
      if (e.key !== "Tab") return;
      const list = items();
      if (list.length === 0) return e.preventDefault();
      const first = list[0], last = list[list.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      previous?.focus();
    };
  }, []);

  return (
    <div className="fixed inset-0 z-10 grid place-items-center bg-slate-900/40 p-4" onMouseDown={onClose}>
      <div ref={box} tabIndex={-1} role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.stopPropagation()}
        className="max-h-[90dvh] w-full max-w-lg overflow-y-auto rounded-2xl bg-white p-6 shadow-xl focus:outline-none">
        <h2 className="mb-4 text-xl font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function ConfirmDialog({ title, message, confirmLabel = "Hapus", busy, onConfirm, onClose }: {
  title: string; message: string; confirmLabel?: string; busy?: boolean; onConfirm: () => void; onClose: () => void;
}) {
  return (
    <Modal title={title} onClose={onClose}>
      <p className="text-sm text-slate-600">{message}</p>
      <div className="flex flex-wrap justify-end gap-2 pt-5">
        <Button onClick={onClose}>Batal</Button>
        <Button variant="danger" onClick={onConfirm} disabled={busy}>{confirmLabel}</Button>
      </div>
    </Modal>
  );
}

export function Field({ label, error, hint, children }: { label: string; error?: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-slate-500">{hint}</span>}
      {error && <span role="alert" className="mt-1 block text-xs text-danger">{error}</span>}
    </label>
  );
}

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" | "danger" };
export function Button({ variant = "ghost", className = "", ...p }: BtnProps) {
  const v = {
    primary: "bg-primary text-white hover:bg-primary-hover",
    ghost: "border border-slate-300 bg-white hover:bg-slate-100",
    danger: "border border-danger/30 bg-white text-danger hover:bg-danger-soft",
  }[variant];
  return <button type="button" {...p} className={`rounded-lg px-3.5 py-2 text-sm font-medium transition-colors disabled:opacity-50 sm:py-1.5 ${v} ${className}`} />;
}

export function ErrorBox({ message }: { message?: string }) {
  return message ? <p role="alert" className="rounded-lg bg-danger-soft px-3 py-2 text-sm text-danger">{message}</p> : null;
}

export function PageHeader({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
      <h2 className="text-xl font-semibold">{title}</h2>
      {action}
    </div>
  );
}

const tones = {
  neutral: "bg-slate-100 text-slate-700",
  dark: "bg-primary text-white",
  green: "bg-ok-soft text-ok",
  red: "bg-danger-soft text-danger",
  amber: "bg-warn-soft text-warn",
};
export function Badge({ tone = "neutral", children }: { tone?: keyof typeof tones; children: ReactNode }) {
  return <span className={`rounded-full px-2.5 py-0.5 font-sans text-xs font-medium ${tones[tone]}`}>{children}</span>;
}

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="rounded-2xl border border-dashed border-slate-300 bg-white px-6 py-12 text-center">
      <p className="font-serif text-lg font-semibold">{title}</p>
      {hint && <p className="mt-1 text-sm text-slate-500">{hint}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export const Loading = () => <p className="text-slate-500">Memuat…</p>;

export const th = "px-3 py-2 text-left text-xs font-medium uppercase tracking-wide text-slate-500";
export const td = "px-3 py-2 text-sm";

//[table] columns = label header, pakai "" untuk kolom aksi; isi tbody lewat children
export function Table({ columns, children }: { columns: string[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-2xl border bg-white shadow-sm">
      <table className="w-full min-w-[40rem]">
        <thead className="bg-slate-50">
          <tr>{columns.map((c, i) => <th key={i} className={th}>{c}</th>)}</tr>
        </thead>
        <tbody className="divide-y">{children}</tbody>
      </table>
    </div>
  );
}
