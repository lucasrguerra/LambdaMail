import { afterAll, beforeAll, describe, expect, it } from "vitest";

process.env.JWT_SECRET ||= "a-test-signing-secret-long-enough-for-the-guard";
process.env.LAMBDAMAIL_MASTER_KEY ||= "test-master-key-at-least-16-chars";
process.env.DATABASE_URL = process.env.TEST_DATABASE_URL ?? "";

const hasDatabase = Boolean(process.env.TEST_DATABASE_URL);
const describeDb = hasDatabase ? describe : describe.skip;

const repo = await import("./repository.js");
const { query, closePool } = await import("./db.js");

// Two tenants. A domain admin of A must never see anything that belongs to B,
// which is what the console's home page did: every figure on it was global.
const A = "aaaaaaaa-0000-0000-0000-00000000000a";
const B = "bbbbbbbb-0000-0000-0000-00000000000b";
const MBX_A = "aaaaaaaa-1111-0000-0000-00000000000a";
const MBX_B1 = "bbbbbbbb-1111-0000-0000-00000000000b";
const MBX_B2 = "bbbbbbbb-2222-0000-0000-00000000000b";
const nameA = `scope-a-${A}.invalid`;
const nameB = `scope-b-${B}.invalid`;

const adminOfA = { role: "DOMAIN_ADMIN", domainId: A };
const superAdmin = { role: "SUPER_ADMIN", domainId: A };

const BLOB = "cccccccc-0000-0000-0000-00000000000c";

async function cleanup(): Promise<void> {
  await query(`DELETE FROM dmarc_reports WHERE domain IN ($1, $2)`, [nameA, nameB]);
  await query(`DELETE FROM outbound_jobs WHERE envelope_from LIKE '%scope-test.invalid'`);
  await query(`DELETE FROM domains WHERE id IN ($1, $2)`, [A, B]);
  await query(`DELETE FROM message_blobs WHERE id = $1`, [BLOB]);
}

describeDb("admin views are confined to the caller's domain", () => {
  beforeAll(async () => {
    await cleanup();
    for (const [id, name] of [[A, nameA], [B, nameB]] as const) {
      await query(
        `INSERT INTO domains (id, name, punycode_name, dns_status) VALUES ($1, $2, $3, 'VERIFIED')`,
        [id, name, name],
      );
    }
    for (const [id, dom, local, quota] of [
      [MBX_A, A, "a1", 1000],
      [MBX_B1, B, "b1", 5000],
      [MBX_B2, B, "b2", 5000],
    ] as const) {
      await query(
        `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, quota_bytes, used_bytes)
         VALUES ($1, $2, $3, $4, 'x', $5, 10)`,
        [id, dom, local, `${local}@${dom === A ? nameA : nameB}`, quota],
      );
    }
    await query(
      `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes)
       VALUES ($1, repeat('c', 64), 'local', 'scope-test', 1)`,
      [BLOB],
    );
    // One job for A, three for B; two of B's are failing.
    for (const [mailbox, status] of [
      [MBX_A, "QUEUED"], [MBX_B1, "QUEUED"], [MBX_B1, "BOUNCED"], [MBX_B2, "DEFERRED"],
    ] as const) {
      await query(
        `INSERT INTO outbound_jobs (blob_id, mailbox_id, envelope_from, envelope_to, destination_domain, status)
         VALUES ($3, $1, 'x@scope-test.invalid', 'to@remote.test', 'remote.test', $2)`,
        [mailbox, status, BLOB],
      );
    }
    // A DMARC report for each domain: one matched by id, one only by name.
    const ra = await query<{ id: string }>(
      `INSERT INTO dmarc_reports (domain_id, domain) VALUES ($1, $2) RETURNING id`, [A, nameA]);
    const rb = await query<{ id: string }>(
      `INSERT INTO dmarc_reports (domain_id, domain) VALUES (NULL, $1) RETURNING id`, [nameB]);
    for (const [report, count] of [[ra[0].id, 7], [rb[0].id, 100]] as const) {
      await query(
        `INSERT INTO dmarc_report_records (report_id, source_ip, count, disposition, dkim_result, spf_result, header_from)
         VALUES ($1, '203.0.113.9', $2, 'none', 'pass', 'pass', 'x.test')`,
        [report, count],
      );
    }
  });

  afterAll(async () => {
    await cleanup();
    await closePool();
  });

  it("the dashboard counts only the admin's own domain", async () => {
    const stats = await repo.dashboardStats(adminOfA);
    expect(stats.domains_total).toBe(1);
    expect(stats.domains_verified).toBe(1);
    expect(stats.mailboxes_active).toBe(1);
    expect(stats.storage_quota_bytes).toBe(1000);
    expect(stats.queue_depth).toBe(1);
  });

  it("a super admin still sees every domain", async () => {
    const stats = await repo.dashboardStats(superAdmin);
    expect(Number(stats.domains_total)).toBeGreaterThanOrEqual(2);
    expect(Number(stats.queue_depth)).toBeGreaterThanOrEqual(3);
  });

  it("the outbound queue hides other tenants' jobs, in the totals and the list", async () => {
    const mine = await repo.queueSummary(adminOfA, { search: "", status: null, offset: 0, pageSize: 50 });
    expect(mine.by_status).toEqual({ QUEUED: 1 });
    expect(mine.total).toBe(1);

    const all = await repo.queueSummary(superAdmin, { search: "scope-test", status: null, offset: 0, pageSize: 50 });
    expect(all.total).toBe(4);
  });

  it("a status filter cannot widen the queue past the domain", async () => {
    const bounced = await repo.queueSummary(adminOfA, { search: "", status: "BOUNCED", offset: 0, pageSize: 50 });
    expect(bounced.total).toBe(0);
  });

  it("DMARC totals come from the admin's own domain only", async () => {
    const mine = await repo.dmarcSummary(adminOfA);
    expect(mine.total_messages).toBe(7);
    const all = await repo.dmarcSummary(superAdmin);
    expect(Number(all.total_messages)).toBeGreaterThanOrEqual(107);
  });

  it("preflight and diagnostics list only the admin's domain", async () => {
    const preflight = (await repo.preflightSummary(adminOfA)) as { checks: { target: string }[] };
    expect(preflight.checks.map((c) => c.target)).toEqual([nameA]);

    const facts = (await repo.serverDiagnosticFacts(adminOfA)) as {
      domains: { name: string }[];
      queueByStatus: Record<string, number>;
    };
    expect(facts.domains.map((d) => d.name)).toEqual([nameA]);
    expect(facts.queueByStatus).toEqual({ QUEUED: 1 });
  });
});
