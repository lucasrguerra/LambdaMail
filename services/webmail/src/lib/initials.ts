/**
 * The two letters an avatar shows.
 *
 * The interface used to print `email[0]`, so every account at a company shared
 * one letter and the avatars stopped distinguishing anything. Two letters are
 * taken from the local part's own word boundaries - `ana.ribeiro@x` gives AR,
 * `operacoes@x` gives OP - which is what the redesign draws.
 *
 * A missing address returns "?" rather than an empty circle: the avatar is a
 * fixed-size element and collapsing it moves the row.
 */
export function initialsFor(address: string | null | undefined): string {
  const local = (address ?? "").split("@")[0];
  const words = local.split(/[._\-+\s]+/).filter(Boolean);

  if (words.length === 0) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();

  return (words[0][0] + words[1][0]).toUpperCase();
}
