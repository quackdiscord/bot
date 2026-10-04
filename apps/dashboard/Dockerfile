# syntax=docker/dockerfile:1
#
# Build from the repository root, since the app uses the shared icon set:
#   docker build -f apps/dashboard/Dockerfile .

FROM node:24-slim AS web
COPY --from=oven/bun:1 /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /src/apps/dashboard
COPY apps/dashboard/package.json apps/dashboard/bun.lock ./
RUN bun install --frozen-lockfile
COPY assets/icons/quack/svg /src/assets/icons/quack/svg
COPY apps/dashboard/ ./
RUN bun run build

FROM golang:1.27.1-alpine AS server
WORKDIR /src
COPY apps/dashboard/go.mod apps/dashboard/*.go ./
COPY --from=web /src/apps/dashboard/dist ./dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/quack-dashboard .

FROM alpine:3.22

RUN addgroup -S quack \
	&& adduser -S -G quack quack \
	&& apk add --no-cache ca-certificates

COPY --from=server /out/quack-dashboard /usr/local/bin/quack-dashboard

USER quack
EXPOSE 3000
ENV QUACK_DASHBOARD_API_URL=http://app:8080

ENTRYPOINT ["/usr/local/bin/quack-dashboard"]
