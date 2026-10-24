import { ApiError } from "./api";

//[error] 502/504 = backend gagal menghubungi database target
export const isConnectionError = (e: unknown) => e instanceof ApiError && (e.status === 502 || e.status === 504);

//[error] pesan yang aman ditampilkan ke user untuk error apa pun
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 401:
        return "Sesi berakhir, silakan masuk lagi.";
      case 403:
        return "Kamu tidak punya izin untuk aksi ini.";
      case 502:
      case 504:
        return `Tidak bisa terhubung ke database target (${e.message}).`;
      case 503:
        return "Layanan belum siap: database platform belum terhubung.";
    }
    return e.message;
  }
  return e instanceof Error ? e.message : "Terjadi kesalahan";
}
