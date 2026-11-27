import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiError } from "./api.ts";
import { errorMessage } from "./errors.ts";

test("502 memakai pesan koneksi target", () => {
  const message = errorMessage(new ApiError(502, "cannot connect to target database"));
  assert.match(message, /Tidak bisa terhubung ke database target/);
});

test("429 memakai pesan rate limit", () => {
  assert.equal(errorMessage(new ApiError(429, "too many attempts")), "Terlalu banyak percobaan. Coba lagi nanti.");
});

test("error non-API memakai pesan Error", () => {
  assert.equal(errorMessage(new Error("boom")), "boom");
});
