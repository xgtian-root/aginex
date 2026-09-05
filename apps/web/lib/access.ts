export type GrantScope = "own" | "all";

export type PrincipalGrant = {
  permission: string;
  scope: GrantScope;
};

export type PrincipalWithGrants = {
  grants: readonly PrincipalGrant[];
};

export function hasGrant(
  principal: PrincipalWithGrants | null | undefined,
  permission: string,
  requiredScope: GrantScope = "all",
): boolean {
  if (!principal) return false;

  return principal.grants.some(
    (grant) =>
      grant.permission === permission &&
      (grant.scope === "all" || grant.scope === requiredScope),
  );
}

export function hasAllGrant(
  principal: PrincipalWithGrants | null | undefined,
  permission: string,
): boolean {
  return hasGrant(principal, permission, "all");
}
