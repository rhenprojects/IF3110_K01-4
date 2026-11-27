import { expect, test } from "@playwright/test";

test("login dan halaman admin users", async ({ page }) => {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error" && !msg.text().includes("401 (Unauthorized)")) errors.push(msg.text());
  });

  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Selamat datang kembali" })).toBeVisible();
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("admin-password");
  await page.getByRole("button", { name: "Masuk" }).click();

  await expect(page).toHaveURL(/\/databases/);
  await page.goto("/admin/users");
  await expect(page.getByRole("heading", { name: "Platform Admin" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();
  expect(errors).toEqual([]);
});
