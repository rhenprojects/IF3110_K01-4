import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { useMigration, useSaveMigration } from "../../features/databases/hooks";
import { Button, ErrorBox, Field, Loading, fieldError, generalError, inputCls } from "../../components/ui";
import { useToast } from "../../components/toast";
import { isDestructive } from "../../lib/format";
import type { DatabaseItem, MigrationInput } from "../../lib/types";
import { StateError, canEdit, useDb } from "./shared";

const FIELDS = ["name", "sql"];

function EditorForm({ db, mid, initial }: { db: DatabaseItem; mid?: string; initial: MigrationInput }) {
  const nav = useNavigate();
  const toast = useToast();
  const save = useSaveMigration(db.id);
  const [name, setName] = useState(initial.name);
  const [sql, setSql] = useState(initial.sql);

  async function submit(e: FormEvent) {
    e.preventDefault();
    try {
      const m = await save.mutateAsync({ mid, input: { name, sql } });
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
        <textarea className={`${inputCls} h-72 font-mono`} value={sql} spellCheck={false}
          placeholder="ALTER TABLE customers ADD COLUMN phone text;" onChange={(e) => setSql(e.target.value)} required />
      </Field>
      {isDestructive(sql) && (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-800">
          SQL ini berisi perintah destruktif (DROP / TRUNCATE / DELETE). Periksa lagi sebelum dijalankan.
        </p>
      )}
      <p className="text-xs text-slate-500">
        Disimpan sebagai draft dulu. Saat dijalankan, seluruh SQL berada dalam satu transaction (COMMIT jika sukses, ROLLBACK jika gagal).
        Perintah yang tidak boleh berada di dalam transaction, seperti CREATE INDEX CONCURRENTLY, akan gagal.
      </p>
      <div className="flex gap-2">
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
      {mid && q.data?.status === "draft" && <EditorForm db={db} mid={mid} initial={{ name: q.data.name, sql: q.data.sql }} />}
      {!mid && <EditorForm db={db} initial={{ name: prefill.name ?? "", sql: prefill.sql ?? "" }} />}
    </div>
  );
}
