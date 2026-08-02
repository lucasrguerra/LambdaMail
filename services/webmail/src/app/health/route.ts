/**
 * Liveness probe for the web container.
 *
 * Deliberately shallow: it answers as soon as the Next server can route a
 * request, and does not reach out to auth, storage or protocols. Those are
 * gated by depends_on and carry probes of their own; folding them in here would
 * mean a blip in one backend marks the frontend unhealthy and takes the whole
 * site down with it.
 *
 * force-dynamic so the answer comes from the running server rather than a
 * response baked in at build time, which would keep replying 200 out of the
 * static cache even if the process were wedged.
 */
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json({ status: "ok", service: "webmail" }, { status: 200 });
}
