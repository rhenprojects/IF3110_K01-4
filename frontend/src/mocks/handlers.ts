import { http, HttpResponse } from "msw";

//[mock] data mock sebelum backend jadi
const user = { id: "u1", name: "Admin Demo", email: "admin@demo.local", isAdmin: true };

//[mock] simpan di sessionStorage agar tahan reload
const isIn = () => sessionStorage.getItem("mock_session") === "1";
const setIn = (v: boolean) => (v ? sessionStorage.setItem("mock_session", "1") : sessionStorage.removeItem("mock_session"));

export const handlers = [
  http.post("/api/v1/users/login", async ({ request }) => {
    const b = (await request.json()) as { email?: string; password?: string };
    if (b.email === user.email && b.password === "password") {
      setIn(true);
      return HttpResponse.json({ message: "Login successful", user });
    }
    return HttpResponse.json({ error: "Invalid email or password" }, { status: 401 });
  }),
  http.get("/api/v1/users/profile", () =>
    isIn() ? HttpResponse.json(user) : HttpResponse.json({ error: "Unauthorized" }, { status: 401 }),
  ),
  http.post("/api/v1/users/logout", () => {
    setIn(false);
    return new HttpResponse(null, { status: 204 });
  }),
];
