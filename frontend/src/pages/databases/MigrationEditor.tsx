import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { useMigration, useSaveMigration } from "../../features/databases/hooks";
import { Button, ErrorBox, Field, Loading, fieldError, generalError, inputCls } from "../../components/ui";
import { useToast } from "../../components/toast";
import { isDestructive } from "../../lib/format";
import type { DatabaseItem, MigrationInput } from "../../lib/types";
import { StateError, canEdit, useDb } from "./shared";

const FIELDS = ["name", "sql", "sql_down", "irreversible_reason"];

function EditorForm({ db, mid, initial }: { db: DatabaseItem; mid?: string; initial: MigrationInput }) {
  const nav = useNavigate();
  const toast = useToast();
  const save = useSaveMigration(db.id);
  const [name, setName] = useState(initial.name);
  const [sql, setSql] = useState(initial.sql);
  const [sqlDown, setSqlDown] = useState(initial.sql_down ?? "");
  const [batchSize, setBatchSize] = useState(String(initial.batch_size ?? 0));
  const [irreversible, setIrreversible] = useState(initial.irreversible ?? false);
  const [reason, setReason] = useState(initial.irreversible_reason ?? "");

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      const input: MigrationInput = irreversible
        ? { name, sql, sql_down: "", irreversible: true, irreversible_reason: reason, batch_size: Number(batchSize) || 0 }
        : { name, sql, sql_down: sqlDown, irreversible: false, irreversible_reason: "", batch_size: Number(batchSize) || 0 };
      const m = await save.mutateAsync({ mid, input });
      toast.success("Draft migration disimpan");
      nav(`/databases/${db.id}/migrations/${m.id}`);
    } catch {
      //[error] ditampilkan lewat save.error
    }
  }

  return (
    <form onSubmit={submit} className="max-w-3xl space-y-4">
      <ErrorBox message={generalError(save.error, FIELDS)} />
      <Field label="Nama migration" error={fieldError(save.error, "name")} hint="Maksimal 200 karakter">
        <input className={inputCls} value={name} maxLength={200} onChange={(e) => setName(e.target.value)} required />
      </Field>
      <Field label="SQL" error={fieldError(save.error, "sql")}
        hint={`${sql.split("\n").length} baris · ${sql.length} karakter`}>
        <textarea wrap="off" className={`${inputCls} h-72 resize-y overflow-auto whitespace-pre font-mono`} value={sql} spellCheck={false}
          placeholder="ALTER TABLE customers ADD COLUMN phone text;" onChange={(e) => setSql(e.target.value)} required />
      </Field>
      {isDestructive(sql) && (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-800">
          SQL ini berisi perintah destruktif (DROP / TRUNCATE / DELETE). Periksa lagi sebelum dijalankan.
        </p>
      )}
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-1" checked={irreversible} onChange={(e) => setIrreversible(e.target.checked)} />
        <span>Migration ini <b>tidak bisa dibatalkan</b> (irreversible), misalnya karena menghapus data permanen.</span>
      </label>
      {irreversible ? (
        <Field label="Alasan irreversible" error={fieldError(save.error, "irreversible_reason")}
          hint="Wajib sebelum migration bisa dijalankan. Migration irreversible tidak bisa di-rollback.">
          <textarea className={`${inputCls} h-20`} value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      ) : (
        <Field label="SQL down (rollback)" error={fieldError(save.error, "sql_down")}
          hint="Membatalkan SQL di atas. Wajib sebelum migration bisa dijalankan.">
          <textarea wrap="off" className={`${inputCls} h-40 resize-y overflow-auto whitespace-pre font-mono`} value={sqlDown} spellCheck={false}
            placeholder="ALTER TABLE customers DROP COLUMN phone;" onChange={(e) => setSqlDown(e.target.value)} />
        </Field>
      )}
      <Field label="Batch size (0 = normal)" error={fieldError(save.error, "batch_size")}
        hint="Isi >0 untuk migration data besar. SQL up wajib memakai :last_id dan :batch_size serta ORDER BY id; SQL down wajib memakai :from_id dan :to_id.">
        <input type="number" min={0} max={1000000} className={inputCls} value={batchSize} onChange={(e) => setBatchSize(e.target.value)} />
      </Field>
      <p className="text-xs text-slate-500">
        Disimpan sebagai draft dulu. Saat dijalankan, migration masuk queue lalu dikerjakan worker; satu database hanya menjalankan
        satu migration pada satu waktu. Seluruh SQL berada dalam satu transaction (COMMIT jika sukses, ROLLBACK jika gagal), jadi
        perintah seperti CREATE INDEX CONCURRENTLY akan gagal. SQL up dan down tidak bisa diubah lagi setelah masuk queue.
      </p>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={save.isPending}>Simpan draft</Button>
        <Button onClick={() => nav(-1)}>Batal</Button>
      </div>
    </form>
  );
}

//[route] /migrations/new (boleh membawa state {name, sql} dari "Salin ke migration baru") dan /migrations/:mid/edit
export default function MigrationEditor() {
  const db = useDb();
  const { mid } = useParams();
  const prefill = (useLocation().state as Partial<MigrationInput> | null) ?? {};
  const q = useMigration(db.id, mid);
  const back = <Link to={`/databases/${db.id}/migrations`} className="underline">← Riwayat migration</Link>;

  //[akses] hanya UX; backend tetap menjawab 403 untuk user View Only
  if (!canEdit(db.level))
    return <p role="alert" className="text-sm text-slate-600">Kamu hanya punya akses View Only pada database ini, jadi tidak bisa membuat atau mengubah migration. {back}</p>;

  return (
    <div>
      <h2 className="mb-4 text-lg font-semibold">{mid ? "Edit draft migration" : "Migration baru"}</h2>
      {mid && q.isLoading && <Loading />}
      {mid && q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {mid && q.data && q.data.status !== "draft" && (
        <p role="alert" className="text-sm text-slate-600">Hanya migration berstatus draft yang bisa diedit. {back}</p>
      )}
      {mid && q.data?.status === "draft" && <EditorForm db={db} mid={mid} initial={q.data} />}
      {!mid && <EditorForm db={db} initial={{ name: "", sql: "", ...prefill }} />}
    </div>
  );
}
