import { describe, it, expect, afterAll } from "vitest";

process.env.LAMBDAMAIL_MASTER_KEY ||= "test-master-key-at-least-16-chars";
process.env.DATABASE_URL = process.env.TEST_DATABASE_URL ?? "";

const hasDatabase = Boolean(process.env.TEST_DATABASE_URL);
const describeDb = hasDatabase ? describe : describe.skip;

const { createAdmin, CliError } = await import("./cli.js");
const { query, closePool } = await import("./db.js");

const DOMAIN = `cli-${Date.now()}.test`;
const EMAIL = `root@${DOMAIN}`;

describeDb("create-admin (command line)", () => {
  afterAll(async () => {
    await query(`DELETE FROM domains WHERE name = $1`, [DOMAIN]);
    await closePool();
  });

  it("gives the first administrator every standard folder, Reports included", async () => {
    const log = console.log;
    console.log = () => undefined;
    try {
      await createAdmin(EMAIL, "a-long-enough-password", true);
    } finally {
      console.log = log;
    }

    // This account used to come out without Reports, so its DMARC and TLS-RPT
    // reports fell into the INBOX: the console and this command each had their
    // own list of folders and the lists had drifted.
    const folders = await query<{ name: string; special_use: string | null }>(
      `SELECT f.name, f.special_use FROM folders f JOIN mailboxes m ON m.id = f.mailbox_id WHERE m.email_address = $1`,
      [EMAIL],
    );
    expect(folders.map((f) => f.name).sort()).toEqual(["Archive", "Drafts", "INBOX", "Junk", "Reports", "Sent", "Trash"]);
    expect(folders.find((f) => f.name === "Reports")?.special_use).toBeNull();
    expect(folders.find((f) => f.name === "Sent")?.special_use).toBe("sent");
  });

  it("refuses with a readable error instead of exiting the process", async () => {
    await expect(createAdmin(EMAIL, "short", true)).rejects.toBeInstanceOf(CliError);
    await expect(createAdmin(EMAIL, "a-long-enough-password", true)).rejects.toThrow(/already exists/);
  });
});
