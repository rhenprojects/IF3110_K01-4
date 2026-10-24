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
