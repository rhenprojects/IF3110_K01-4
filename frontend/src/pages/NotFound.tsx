import { Link } from "react-router-dom";

export default function NotFound() {
  return (
    <div className="grid min-h-[50vh] place-items-center text-center">
      <div>
        <h1 className="text-2xl font-semibold">404</h1>
        <p className="text-slate-500">Halaman tidak ditemukan.</p>
        <Link to="/databases" className="mt-3 inline-block text-sm underline">Kembali ke Databases</Link>
      </div>
    </div>
  );
}
