# The migrations travel inside an image rather than a bind mount.
#
# A deployment that pulls its images has no reason to also depend on files
# being present at a particular path on the host: the orchestrator rewrites
# relative bind mounts to its own layout, and an empty directory silently
# becomes "no migrations found" - which fails the deploy with no useful error.
#
# Build context is the repository root.
FROM migrate/migrate:v4.17.1

COPY migrations /migrations

ENTRYPOINT ["migrate", "-path=/migrations"]
