//[filter] filter pill dengan satu pilihan aktif; count opsional menunjukkan jumlah item per pilihan
export function FilterChips<T extends string>({ label, value, onChange, options }: {
  label: string; value: T; onChange: (v: T) => void; options: { value: T; label: string; count?: number }[];
}) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button key={o.value} type="button" aria-pressed={on} onClick={() => onChange(o.value)}
            className={`rounded-full border px-4 py-2 text-sm font-medium transition-colors sm:py-1.5 ${on ? "border-primary bg-primary text-white" : "bg-white text-slate-600 hover:bg-slate-100"}`}>
            {o.label}
            {o.count != null && <span className={`ml-1.5 ${on ? "text-white/80" : "text-slate-500"}`}>{o.count}</span>}
          </button>
        );
      })}
    </div>
  );
}

//[filter] filter on/off (checkbox sungguhan, jadi tetap bisa dipakai lewat keyboard dan pembaca layar)
export function ToggleChip({ label, hint, checked, onChange }: {
  label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <label title={hint}
      className="inline-flex cursor-pointer items-center gap-2 rounded-full border bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-100 has-[:checked]:border-primary has-[:checked]:bg-primary-soft has-[:checked]:text-primary has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-primary sm:py-1.5">
      <input type="checkbox" className="size-4 accent-primary" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}
