import type { GrantScope, PrincipalGrant } from "@/lib/access";

export type PermissionOption = {
  code: string;
  allowedScopes: readonly string[];
};

export type CurrentRoleGrant = {
  permission: { code: string };
  scope: GrantScope;
};

export function groupPermissionsByResource<T extends { code: string }>(
  permissions: T[],
): [string, T[]][] {
  const grouped = new Map<string, T[]>();
  for (const permission of permissions) {
    const resource = permission.code.split(":", 1)[0] || permission.code;
    const current = grouped.get(resource) ?? [];
    current.push(permission);
    grouped.set(resource, current);
  }
  return [...grouped.entries()]
    .sort(([first], [second]) => first.localeCompare(second))
    .map(([resource, options]) => [
      resource,
      options.sort((first, second) => first.code.localeCompare(second.code)),
    ]);
}

export function preferredScope(scopes: readonly GrantScope[]): GrantScope {
  return scopes.includes("own") ? "own" : "all";
}

export function delegableScopes(
  permission: PermissionOption,
  actorGrants: readonly PrincipalGrant[],
): GrantScope[] {
  const actorGrant = actorGrants.find(
    (grant) => grant.permission === permission.code,
  );
  if (!actorGrant) return [];
  return permission.allowedScopes.filter(
    (scope): scope is GrantScope =>
      (scope === "own" || scope === "all") &&
      (actorGrant.scope === "all" || scope === "own"),
  );
}

export function sameGrants(
  proposed: { permissionCode: string; scope: GrantScope }[],
  current: readonly CurrentRoleGrant[],
): boolean {
  const left = proposed
    .map((grant) => `${grant.permissionCode}:${grant.scope}`)
    .sort();
  const right = current
    .map((grant) => `${grant.permission.code}:${grant.scope}`)
    .sort();
  return left.join("\u0000") === right.join("\u0000");
}
