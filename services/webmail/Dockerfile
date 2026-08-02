FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:20-alpine
# The node:20-alpine tag lags its Alpine base, so pending security patches
# (openssl, busybox) land only with an explicit upgrade.
RUN apk upgrade --no-cache \
    # The runtime only ever runs "node"; npm is not invoked and its own
    # bundled dependency tree (sigstore, cross-spawn, glob, ...) is what
    # every remaining image CVE came from. Removing it drops the whole
    # class instead of chasing each advisory.
    && rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx
WORKDIR /app
# --chown matters here: Next's standalone server writes its cache under
# .next at runtime, which it cannot do if the tree belongs to root.
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
COPY --from=build --chown=node:node /app/public ./public

# The node images ship an unprivileged "node" user (uid 1000).
USER node
EXPOSE 3000
# Docker sets HOSTNAME to the container ID by default; Next's standalone
# server.js honors that env var and binds only to it, refusing localhost
# (and thus the healthcheck). Force it to listen on every interface.
ENV HOSTNAME=0.0.0.0
CMD ["node", "server.js"]
