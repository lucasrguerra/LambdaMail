/**
 * A role as a person reads it.
 *
 * The console printed the stored constants - USER, DOMAIN_ADMIN, SUPER_ADMIN -
 * straight onto the screen, so those three words stayed in English whatever
 * the interface was set to.
 */
export function roleLabel(t: (key: string) => string, role: string): string {
  const known: Record<string, string> = {
    USER: "roles.user",
    DOMAIN_ADMIN: "roles.domainAdmin",
    SUPER_ADMIN: "roles.superAdmin",
  };
  const key = known[String(role).toUpperCase()];
  // An unknown role is shown as stored rather than as a missing-translation
  // marker: whoever sees it needs to know what the database actually holds.
  return key ? t(key) : String(role);
}
