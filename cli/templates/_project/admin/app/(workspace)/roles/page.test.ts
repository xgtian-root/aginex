import { describe, expect, it } from "vitest";
import {
  delegableScopes,
  groupPermissionsByResource,
  preferredScope,
  sameGrants,
} from "./helpers";

const filesRead = {
  id: "permission-files-read",
  code: "files:read",
  description: "Read files",
  allowedScopes: ["own", "all"],
  createdAt: "2026-08-10T00:00:00Z",
};
const usersRead = {
  id: "permission-users-read",
  code: "users:read",
  description: "Read users",
  allowedScopes: ["all"],
  createdAt: "2026-08-10T00:00:00Z",
};

describe("Access permission editor", () => {
  it("groups and sorts permissions by resource", () => {
    const groups = groupPermissionsByResource([usersRead, filesRead] as never);

    expect(groups.map(([resource]) => resource)).toEqual(["files", "users"]);
    expect(groups[0]?.[1].map((permission) => permission.code)).toEqual([
      "files:read",
    ]);
  });

  it("defaults to the least scope supported by the permission", () => {
    expect(preferredScope(["own", "all"])).toBe("own");
    expect(preferredScope(["all"])).toBe("all");
  });

  it("caps delegated scopes at the actor's effective grant", () => {
    expect(
      delegableScopes(filesRead as never, [
        { permission: "files:read", scope: "own" },
      ]),
    ).toEqual(["own"]);
    expect(
      delegableScopes(filesRead as never, [
        { permission: "files:read", scope: "all" },
      ]),
    ).toEqual(["own", "all"]);
    expect(delegableScopes(usersRead as never, [])).toEqual([]);
  });

  it("compares role grants without depending on order", () => {
    expect(
      sameGrants(
        [
          { permissionCode: "users:read", scope: "all" },
          { permissionCode: "files:read", scope: "own" },
        ],
        [
          { permission: filesRead, scope: "own" },
          { permission: usersRead, scope: "all" },
        ] as never,
      ),
    ).toBe(true);
  });
});
