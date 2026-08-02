# The migrations travel inside an image rather than a bind mount.
#
# A deployment that pulls its images has no reason to also depend on files
# being present at a particular path on the host: the orchestrator rewrites
# relative bind mounts to its own layout, and an empty directory silently
# becomes "no migrations found" - which fails the deploy with no useful error.
#
# Build context is the repository root.
# v4.19.1: the v4.17.1 image is built on Alpine 3.19 and carries six HIGH OS
# advisories plus a long tail in its own binary.
FROM migrate/migrate:v4.19.1

# Pending OS patches land only with an explicit upgrade.
RUN apk upgrade --no-cache

COPY migrations /migrations

# Applying a schema needs a database connection, not root on the container.
RUN addgroup -g 10002 -S migrate && adduser -u 10002 -S -G migrate migrate
USER migrate:migrate

ENTRYPOINT ["migrate", "-path=/migrations"]
