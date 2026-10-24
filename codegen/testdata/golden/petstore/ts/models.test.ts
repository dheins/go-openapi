// Tests of the generated TypeScript models and the runtime's validator
// and encoder. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import * as m from "./models.ts";
import { encode, validate, type Issue } from "./runtime.ts";

const pointers = (issues: Issue[]) => issues.map((i) => i.pointer + ": " + i.message);

test("a valid pet, and the read-only id by direction", () => {
  const pet: m.Pet = { name: "Rex", status: m.PetStatus.Available, tag: null, size: m.PetSize.Medium };
  assert.deepEqual(validate(m.PetSchema, pet, "request"), []);
  assert.deepEqual(pointers(validate(m.PetSchema, pet, "response")), ["/id: required property is missing"]);
  assert.deepEqual(validate(m.PetSchema, { ...pet, id: 7 }, "response"), []);
});

test("the constraints of a pet", () => {
  const bad = {
    name: "rex1",
    status: "lost",
    weight: 0.3,
    photoUrls: ["a", "a"],
    size: 4,
    owner: {},
  };
  assert.deepEqual(pointers(validate(m.PetSchema, bad, "request")), [
    "/name: must match the pattern ^[A-Za-z][A-Za-z ]*$",
    '/status: must be one of "available", "pending", "sold"',
    "/weight: must be a multiple of 0.5",
    "/photoUrls: must not have duplicate items",
    "/owner/email: required property is missing",
    "/size: must be one of 1, 2, 3",
  ]);
  assert.deepEqual(pointers(validate(m.PetSchema, { name: "", status: "sold", photoUrls: [] }, "request")), [
    "/name: must be at least 1 characters long",
    "/name: must match the pattern ^[A-Za-z][A-Za-z ]*$",
    "/photoUrls: must have at least 1 items",
  ]);
  assert.deepEqual(pointers(validate(m.PetSchema, "Rex", "request")), [": expected object, got string"]);
});

test("a recursive type", () => {
  assert.deepEqual(validate(m.NodeSchema, { value: "a", next: { value: "b", next: null } }, "request"), []);
  assert.deepEqual(pointers(validate(m.NodeSchema, { value: "a", next: { value: 1 } }, "request")), [
    "/next/value: expected string, got number",
    "/next/next: required property is missing",
  ]);
});

test("a discriminated union", () => {
  assert.deepEqual(validate(m.VehicleSchema, { kind: "Bike", wheels: 2, gears: 7 }, "request"), []);
  // The discriminator chooses the variant the value is checked against.
  assert.deepEqual(pointers(validate(m.VehicleSchema, { kind: "automobile", wheels: 4, doors: 6 }, "request")), ["/doors: must be <= 5"]);
  assert.deepEqual(pointers(validate(m.VehicleSchema, { kind: "Bike", wheels: 2 }, "request")), ["/gears: required property is missing"]);
  assert.deepEqual(pointers(validate(m.GarageSchema, { vehicles: [{ kind: "Car", wheels: -1 }] }, "request")), ["/vehicles/0/wheels: must be >= 0"]);
});

test("a union without a discriminator", () => {
  assert.deepEqual(validate(m.IDOrNameSchema, 5, "request"), []);
  assert.deepEqual(validate(m.IDOrNameSchema, "ab", "request"), []);
  assert.deepEqual(pointers(validate(m.IDOrNameSchema, "a", "request")), [": matches none of the variants"]);
  assert.deepEqual(pointers(validate(m.IDOrNameSchema, true, "request")), [": matches none of the variants"]);
});

test("checks from allOf, property counts and additional properties", () => {
  assert.deepEqual(pointers(validate(m.BadgeSchema, { code: "AB" }, "request")), ["/code: must be at least 3 characters long"]);
  assert.deepEqual(pointers(validate(m.BadgeSchema, { code: "abcd" }, "request")), ["/code: must match the pattern ^[A-Z]+$"]);
  assert.deepEqual(pointers(validate(m.ExtensionsSchema, { name: "a", other: 1 }, "request")), ["/other: unknown property"]);
  assert.deepEqual(pointers(validate(m.ExtensibleSchema, { kind: "k", a: 1, b: -1 }, "request")), ["/b: must be >= 0"]);
  assert.deepEqual(pointers(validate(m.AccountSchema, { username: "u" }, "request")), [
    ": must have at least 3 properties",
    "/password: required property is missing",
  ]);
});

test("encoding leaves out read-only and write-only properties", () => {
  const account = { id: 1, username: "u", password: "p", role: "admin" };
  assert.deepEqual(encode(m.AccountSchema, account, "request"), { username: "u", password: "p", role: "admin" });
  assert.deepEqual(encode(m.AccountSchema, account, "response"), { id: 1, username: "u", role: "admin" });
  // At any depth, and through unions: the variant is found.
  const pets = [{ id: 1, name: "Rex", status: "sold" }];
  assert.deepEqual(encode({ kind: "array", items: m.PetSchema }, pets, "request"), [{ name: "Rex", status: "sold" }]);
});

test("enums have constant objects", () => {
  assert.equal(m.PetStatus.Pending, "pending");
  assert.deepEqual(Object.values(m.PetSize), [1, 2, 3]);
});
