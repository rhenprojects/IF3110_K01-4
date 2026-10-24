//[types] bentuk data sesuai docs/api-contract.md

export type User = {
  id: string;
  username: string;
  name: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
};
export type CreateUserInput = { username: string; name?: string; password: string; is_admin?: boolean };
export type UpdateUserInput = { name?: string; password?: string; is_admin?: boolean };

export type Group = {
  id: string;
  name: string;
  description: string;
  member_ids: string[];
  created_at: string;
  updated_at: string;
};
export type GroupInput = { name: string; description?: string };

export type SSLMode = "disable" | "allow" | "prefer" | "require" | "verify-ca" | "verify-full";
export type Connection = {
  id: string;
  name: string;
  host: string;
  port: number;
  db_name: string;
  username: string;
  ssl_mode: SSLMode;
  created_by: string;
  created_at: string;
  updated_at: string;
};
//[input] password wajib saat create, opsional saat update (kosong = tidak diubah)
export type ConnectionInput = {
  name: string;
  host: string;
  port?: number;
  db_name: string;
  username: string;
  password?: string;
  ssl_mode?: SSLMode;
};
export type TestResult = { status: "ok"; server_version: string };

export type AccessLevel = "view" | "edit";
export type AccessEntry = { connection_id: string; level: AccessLevel };

//[databases] endpoint user biasa (docs/api-contract.md bagian 8)
export type DatabaseItem = { id: string; name: string; db_name: string; level: AccessLevel };

export type ColumnInfo = { name: string; type: string; nullable: boolean; default: string | null; is_primary_key: boolean };
export type ConstraintInfo = { name: string; type: "primary_key" | "foreign_key" | "unique" | "check"; definition: string };
export type IndexInfo = { name: string; is_unique: boolean; definition: string };
export type TableInfo = {
  name: string;
  type: "table" | "view";
  columns: ColumnInfo[];
  constraints: ConstraintInfo[];
  indexes: IndexInfo[];
};
export type SchemaInfo = { name: string; tables: TableInfo[] };
export type SchemaResponse = { schemas: SchemaInfo[] };
//[rows] tiap baris = array, urutan sama dengan columns; NULL dikirim sebagai null
export type RowsResponse = {
  columns: { name: string; type: string }[];
  rows: unknown[][];
  total: number;
  limit: number;
  offset: number;
};

//[migration] docs/api-contract.md bagian 9
export type MigrationStatus = "draft" | "success" | "failed";
export type Migration = {
  id: string;
  connection_id: string;
  version: number | null; //[migration] hanya terisi untuk status success
  name: string;
  sql: string;
  status: MigrationStatus;
  error: string;
  created_by: string;
  created_by_name: string;
  executed_by: string | null;
  executed_by_name: string | null;
  created_at: string;
  updated_at: string;
  executed_at: string | null;
  duration_ms: number | null;
};
export type MigrationInput = { name: string; sql: string };
