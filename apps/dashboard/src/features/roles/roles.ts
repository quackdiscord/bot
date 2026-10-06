import type { DirectoryRole } from "~/api/types";

/**
 * roleColor is a Discord role color as CSS, or null for a role without
 * one (Discord stores "no color" as 0).
 */
export function roleColor(color: number): string | null {
  if (!color) return null;
  return `#${(color & 0xffffff).toString(16).padStart(6, "0")}`;
}

/**
 * pickableRoles is what a role picker offers: the roles not already
 * chosen whose name contains query, ignoring case, highest first as the
 * API returns them.
 */
export function pickableRoles(
  roles: readonly DirectoryRole[],
  chosen: readonly string[],
  query: string,
): DirectoryRole[] {
  const needle = query.trim().toLowerCase();
  return roles.filter(
    (role) => !chosen.includes(role.id) && (!needle || role.name.toLowerCase().includes(needle)),
  );
}
