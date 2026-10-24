import { useEffect, type ButtonHTMLAttributes, type ReactNode } from "react";
import { ApiError } from "../lib/api";

export const inputCls = "mt-1 w-full rounded border border-slate-300 px-3 py-2 text-sm focus:outline-2 focus:outline-slate-900";

//[error] pesan error field tertentu dari backend (400 + field)
export const fieldError = (e: unknown, field: string) =>
  e instanceof ApiError && e.field === field ? e.message : undefined;

//[error] pesan umum: tampil jika bukan error field yang ditangani form
export const generalError = (e: unknown, fields: string[]) => {
  if (!e) return undefined;
  if (e instanceof ApiError && e.field && fields.includes(e.field)) return undefined;
  return e instanceof Error ? e.message : "Terjadi kesalahan";
};

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-10 grid place-items-center bg-black/40 p-4" onMouseDown={onClose}>
      <div role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.stopPropagation()}
        className="max-h-[90vh] w-full max-w-lg overflow-y-auto rounded-lg bg-white p-5 shadow-xl">
        <h2 className="mb-4 text-lg font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function Field({ label, error, hint, children }: { label: string; error?: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-slate-500">{hint}</span>}
      {error && <span role="alert" className="mt-1 block text-xs text-red-600">{error}</span>}
    </label>
  );
}

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" | "danger" };
export function Button({ variant = "ghost", className = "", ...p }: BtnProps) {
  const v = {
    primary: "bg-slate-900 text-white hover:bg-slate-700",
    ghost: "border border-slate-300 hover:bg-slate-100",
    danger: "border border-red-300 text-red-700 hover:bg-red-50",
  }[variant];
  return <button type="button" {...p} className={`rounded px-3 py-1.5 text-sm disabled:opacity-50 ${v} ${className}`} />;
}

export function ErrorBox({ message }: { message?: string }) {
  return message ? <p role="alert" className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">{message}</p> : null;
}

export function PageHeader({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="mb-4 flex items-center justify-between">
      <h2 className="text-lg font-semibold">{title}</h2>
      {action}
    </div>
  );
}

export const th = "px-3 py-2 text-left text-xs font-medium uppercase tracking-wide text-slate-500";
export const td = "px-3 py-2 text-sm";
