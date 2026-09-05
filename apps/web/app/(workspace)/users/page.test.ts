import { describe, expect, it } from "vitest";
import { sameIDs } from "./helpers";

describe("People role selection", () => {
  it("treats the same role ids as unchanged regardless of order", () => {
    expect(sameIDs(["role-b", "role-a"], ["role-a", "role-b"])).toBe(true);
  });

  it("detects a changed role assignment", () => {
    expect(sameIDs(["role-a"], ["role-a", "role-b"])).toBe(false);
  });
});
