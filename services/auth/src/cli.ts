/**
 * Operator commands for a fresh deployment.
 *
 * A new install has no domain and no mailbox, and every way into the admin
 * console needs one - so without this there is no first login. This is a
 * deliberate command rather than a bootstrap on boot: creating an
 * administrator as a side effect of starting a container is the kind of thing
 * that quietly recreates a deleted account.
 *
 * Usage:
 *   node dist/cli.js create-admin <email> <password> [--force]
 *   node dist/cli.js reset-password <email> <password>
 *   node dist/cli.js reset-mfa <email>
 */

import { query, queryOne, closePool } from "./db.js";
import { hashPassword } from "./crypto.js";

const SYSTEM_ALIASES = ["postmaster", "abuse", "dmarc", "tlsrpt"];
const STANDARD_FOLDERS: Array<[string, string]> = [
  ["INBOX", "inbox"],
  ["Sent", "sent"],
  ["Drafts", "drafts"],
  ["Trash", "trash"],
  ["Junk", "junk"],
  ["Archive", "archive"],
];

function fail(message: string): never {
  console.error(`error: ${message}`);
  process.exit(1);
}

function parseAddress(email: string): { localPart: string; domain: string } {
  const at = email.lastIndexOf("@");
  if (at <= 0 || at === email.length - 1) fail(`"${email}" is not an email address`);
  return { localPart: email.slice(0, at).toLowerCase(), domain: email.slice(at + 1).toLowerCase() };
}

async function createAdmin(email: string, password: string, force: boolean): Promise<void> {
  if (password.length < 12) fail("the password must be at least 12 characters");
  const { localPart, domain } = parseAddress(email);

  const existingAdmins = await queryOne<{ count: string }>(
    `SELECT count(*)::text AS count FROM mailboxes WHERE role IN ('SUPER_ADMIN','DOMAIN_ADMIN')`,
  );
  if (Number(existingAdmins?.count ?? 0) > 0 && !force) {
    fail("this deployment already has an administrator; pass --force to add another");
  }

  let domainRow = await queryOne<{ id: string }>(`SELECT id FROM domains WHERE name = $1`, [domain]);
  if (!domainRow) {
    // name is CITEXT and punycode_name is VARCHAR, so the value is bound twice.
    domainRow = await queryOne<{ id: string }>(
      `INSERT INTO domains (name, punycode_name) VALUES ($1, $2) RETURNING id`,
      [domain, domain],
    );
    console.log(`created domain ${domain}`);
  }
  const domainId = domainRow!.id;

  const existing = await queryOne<{ id: string }>(`SELECT id FROM mailboxes WHERE email_address = $1`, [email]);
  if (existing) fail(`${email} already exists`);

  const mailbox = await queryOne<{ id: string }>(
    `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
     VALUES ($1, $2, $3, $4, 'SUPER_ADMIN') RETURNING id`,
    [domainId, localPart, email, await hashPassword(password)],
  );

  // Delivery needs these: a mailbox with no INBOX cannot receive at all, and
  // one with no Junk used to fail the spam path outright.
  for (const [name, specialUse] of STANDARD_FOLDERS) {
    await query(
      `INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, $2, $3)`,
      [mailbox!.id, name, specialUse],
    );
  }

  // PLAN.md section 7.4b: the published DNS records name these, and reports
  // bounce if they do not resolve.
  for (const local of SYSTEM_ALIASES) {
    await query(
      `INSERT INTO aliases (domain_id, source_address, destination_addresses, is_system)
       VALUES ($1, $2, $3, true) ON CONFLICT (domain_id, source_address) DO NOTHING`,
      [domainId, `${local}@${domain}`, [email]],
    );
  }

  console.log(`created administrator ${email}`);
  console.log(`system aliases: ${SYSTEM_ALIASES.map((a) => `${a}@${domain}`).join(", ")}`);
  console.log("");
  console.log("Sign in at /admin/login. If the account has no second factor yet, the");
  console.log("console enrolls one there and then - no need to visit another screen.");
}

async function resetPassword(email: string, password: string): Promise<void> {
  if (password.length < 12) fail("the password must be at least 12 characters");

  const rows = await query<{ id: string }>(
    `UPDATE mailboxes SET password_hash = $2, password_updated_at = NOW(), failed_login_count = 0,
            locked_until = NULL, updated_at = NOW()
      WHERE email_address = $1 RETURNING id`,
    [email.toLowerCase(), await hashPassword(password)],
  );
  if (rows.length === 0) fail(`${email} was not found`);

  // Every existing session is dropped: a reset that leaves them alive has not
  // locked anyone out.
  await query(`UPDATE web_sessions SET revoked_at = NOW() WHERE mailbox_id = $1 AND revoked_at IS NULL`, [rows[0].id]);
  console.log(`password reset for ${email}; all its sessions were signed out`);
}

/**
 * Removes every second factor from an account, so the next sign-in enrolls a
 * new one.
 *
 * This is the lost-phone command, and the answer to a subtler case: an
 * enrollment can be confirmed against a secret the authenticator no longer
 * holds, and the account is then locked behind codes nobody can produce. The
 * alternative - deleting the mailbox and recreating it - would take its mail,
 * folders and aliases with it for no reason.
 *
 * The password is untouched, so this alone does not grant anyone access.
 * Sessions are revoked because a live session would otherwise keep the console
 * open without the factor that was just removed.
 */
async function resetMfa(email: string): Promise<void> {
  const mailbox = await queryOne<{ id: string }>(
    `SELECT id FROM mailboxes WHERE email_address = $1`,
    [email.toLowerCase()],
  );
  if (!mailbox) fail(`${email} was not found`);

  const totp = await query<{ id: string }>(
    `DELETE FROM mfa_totp WHERE mailbox_id = $1 RETURNING id`, [mailbox!.id]);
  const codes = await query<{ id: string }>(
    `DELETE FROM mfa_recovery_codes WHERE mailbox_id = $1 RETURNING id`, [mailbox!.id]);
  await query(
    `UPDATE web_sessions SET revoked_at = NOW() WHERE mailbox_id = $1 AND revoked_at IS NULL`,
    [mailbox!.id],
  );

  console.log(`removed ${totp.length} authenticator secret(s) and ${codes.length} recovery code(s) for ${email}`);
  console.log("its sessions were signed out; the password is unchanged");
  console.log("");
  console.log("Sign in at /admin/login and the console will enroll a new factor.");
}

async function main(): Promise<void> {
  const [command, ...args] = process.argv.slice(2);

  if (!process.env.DATABASE_URL) fail("DATABASE_URL is not set");
  if (!process.env.LAMBDAMAIL_MASTER_KEY) fail("LAMBDAMAIL_MASTER_KEY is not set");

  switch (command) {
    case "create-admin":
      if (args.length < 2) fail("usage: create-admin <email> <password> [--force]");
      await createAdmin(args[0].toLowerCase(), args[1], args.includes("--force"));
      break;
    case "reset-password":
      if (args.length < 2) fail("usage: reset-password <email> <password>");
      await resetPassword(args[0], args[1]);
      break;
    case "reset-mfa":
      if (args.length < 1) fail("usage: reset-mfa <email>");
      await resetMfa(args[0]);
      break;
    default:
      console.log("commands: create-admin <email> <password> [--force]");
      console.log("          reset-password <email> <password>");
      console.log("          reset-mfa <email>");
      process.exit(command ? 1 : 0);
  }
}

main()
  .then(() => closePool())
  .catch(async (err) => {
    console.error(`error: ${err instanceof Error ? err.message : String(err)}`);
    await closePool().catch(() => undefined);
    process.exit(1);
  });
