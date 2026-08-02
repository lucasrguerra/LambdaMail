FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
# npm ci, not npm install: the image must be the lockfile's content, not
# whatever resolves the day it is built. --include=dev because this stage
# needs typescript, and npm omits devDependencies when NODE_ENV=production -
# which the deployment sets.
RUN npm ci --include=dev
COPY . .
RUN npm run build

# Production dependencies only. The previous image copied the build stage's
# node_modules wholesale, shipping typescript, eslint and vitest into
# production - most of the CVEs the image scan reported came from that
# tooling, which never runs in the container.
FROM node:20-alpine AS prod-deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev

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
COPY --from=prod-deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./

# Created before dropping privileges so a fresh named volume mounted here
# inherits the ownership: Docker seeds an empty volume from the image.
RUN mkdir -p /var/lambdamail/mails && chown -R node:node /var/lambdamail

# The node images ship an unprivileged "node" user (uid 1000).
USER node
EXPOSE 3002
CMD ["node", "dist/server.js"]
