# The operators' web panel (cmd/panel): the web client (web/panel) is built
# with node, then embedded into the Go binary (-tags panelembed), so the
# running image holds one static binary and nothing else.
#
#   docker compose build panel
FROM node:22-alpine AS web
WORKDIR /web
COPY web/panel/package.json web/panel/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/panel/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/panel/dist
RUN CGO_ENABLED=0 go build -tags panelembed -trimpath -ldflags="-s -w" -o /out/panel ./cmd/panel

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/panel /panel
USER app
EXPOSE 8080
ENTRYPOINT ["/panel"]
