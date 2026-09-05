export function sameIDs(first: string[], second: string[]): boolean {
  return [...first].sort().join("\u0000") === [...second].sort().join("\u0000");
}
