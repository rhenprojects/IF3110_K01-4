# web (frontend)

React + Vite + TypeScript. Dev: `cp .env.example .env && npm install && npm run dev` (http://localhost:5173).

- `VITE_USE_MOCK=true`: pakai MSW. Login demo: `admin` / `password` (admin), `budi` / `password` (non-admin).
- `VITE_USE_MOCK=false`: `/api` di-proxy ke backend Go (`localhost:9000`).
