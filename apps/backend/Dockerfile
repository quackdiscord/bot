# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/quack ./cmd/quack

FROM alpine:3.22

RUN addgroup -S quack \
	&& adduser -S -G quack quack \
	&& apk add --no-cache ca-certificates curl tzdata

WORKDIR /app
COPY --from=build /out/quack /usr/local/bin/quack

USER quack
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/quack"]
CMD ["serve"]
