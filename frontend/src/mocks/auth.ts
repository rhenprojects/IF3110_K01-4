import { http, HttpResponse } from "msw";
import { load, publicUser, sessionUserId, setSession } from "./db";

//[error] format error: { error, field? }
export const err = (status: number, error: string, field?: string) =>
  HttpResponse.json(field ? { error, field } : { error }, { status });

export function currentUser() {
  const id = sessionUserId();
  return load().users.find((u) => u.id === id) ?? null;
}

export const authHandlers = [
  http.post("/api/v1/users/login", async ({ request }) => {
    const b = (await request.json()) as { username?: string; password?: string };
    const u = load().users.find((x) => x.username === b.username && x.password === b.password);
    if (!u) return err(401, "Invalid username or password");
    setSession(u.id);
    return HttpResponse.json({ message: "Login successful", user: publicUser(u) });
  }),
  http.post("/api/v1/users/logout", () => {
    setSession(null);
    return new HttpResponse(null, { status: 204 });
  }),
  http.get("/api/v1/users/profile", () => {
    const u = currentUser();
    return u ? HttpResponse.json(publicUser(u)) : err(401, "Unauthorized");
  }),
];
