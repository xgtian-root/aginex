import { describe, expect, it } from "vitest";
import { hasAllGrant, hasGrant } from "./access";

const principal = {
  grants: [
    { permission: "files:read", scope: "own" as const },
    { permission: "users:update", scope: "all" as const },
  ],
};

describe("access grants", () => {
  it("lets an all-scope grant satisfy own and all checks", () => {
    expect(hasGrant(principal, "users:update", "own")).toBe(true);
    expect(hasAllGrant(principal, "users:update")).toBe(true);
  });

  it("does not let an own-scope grant expose an all-scope action", () => {
    expect(hasGrant(principal, "files:read", "own")).toBe(true);
    expect(hasAllGrant(principal, "files:read")).toBe(false);
  });

  it("fails closed for missing principals and permissions", () => {
    expect(hasAllGrant(undefined, "users:update")).toBe(false);
    expect(hasAllGrant(principal, "roles:grant")).toBe(false);
  });
});
