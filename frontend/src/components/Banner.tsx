import type { ReactNode } from "react";

//[banner] tab di dalam banner hijau: aktif = putih, lainnya transparan
export const bannerTab = ({ isActive }: { isActive: boolean }) =>
  `shrink-0 whitespace-nowrap rounded-full px-4 py-1.5 text-sm font-medium transition-colors ${isActive ? "bg-white text-primary shadow-sm" : "text-white/85 hover:bg-white/15"}`;

//[banner] header halaman: gradien hijau + ornamen lingkaran; children dipakai untuk tab atau ringkasan angka
export function Banner({ eyebrow, title, meta, caption, icon, action, children }: {
  eyebrow?: ReactNode; title: string; meta?: ReactNode; caption?: string; icon?: ReactNode; action?: ReactNode; children?: ReactNode;
}) {
  return (
    <section className="relative mb-6 overflow-hidden rounded-3xl bg-gradient-to-br from-primary-light via-primary to-primary-deep p-6 text-white shadow-sm sm:p-8">
      <svg aria-hidden viewBox="0 0 400 240" preserveAspectRatio="xMaxYMid slice" className="pointer-events-none absolute inset-y-0 right-0 h-full w-auto opacity-25">
        <g fill="none" stroke="white" strokeWidth="1">
          <circle cx="340" cy="60" r="40" /><circle cx="340" cy="60" r="80" /><circle cx="340" cy="60" r="120" /><circle cx="340" cy="60" r="160" />
        </g>
        <g fill="white"><circle cx="250" cy="190" r="3" /><circle cx="280" cy="210" r="2" /><circle cx="370" cy="200" r="4" /><circle cx="220" cy="150" r="2" /></g>
      </svg>
      <div className="relative space-y-4">
        {eyebrow && <div className="text-sm text-white/80">{eyebrow}</div>}
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex min-w-0 items-center gap-4">
            {icon && <span className="grid size-12 shrink-0 place-items-center rounded-2xl bg-white/15 ring-1 ring-white/25">{icon}</span>}
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <h1 className="min-w-0 break-words text-3xl font-semibold">{title}</h1>
                {meta}
              </div>
              {caption && <p className="mt-1 max-w-2xl text-sm text-white/85">{caption}</p>}
            </div>
          </div>
          {action}
        </div>
        {children}
      </div>
    </section>
  );
}

//[banner] chip ringkasan angka di bawah judul banner
export const BannerStat = ({ value, label }: { value: number | string; label: string }) => (
  <div className="rounded-2xl bg-white/15 px-4 py-2 ring-1 ring-white/20">
    <span className="font-serif text-xl font-semibold">{value}</span> <span className="text-sm text-white/85">{label}</span>
  </div>
);
