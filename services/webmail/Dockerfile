FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
RUN npm run build

FROM node:20-alpine
WORKDIR /app
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
EXPOSE 3000
# Docker sets HOSTNAME to the container ID by default; Next's standalone
# server.js honors that env var and binds only to it, refusing localhost
# (and thus the healthcheck). Force it to listen on every interface.
ENV HOSTNAME=0.0.0.0
CMD ["node", "server.js"]
