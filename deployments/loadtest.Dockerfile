# Builds one of the throwaway test binaries under tests/loadtest (never a
# production service): PKG selects the package, e.g.
#   --build-arg PKG=./tests/loadtest/mocktelegram
# Kept separate from deployments/Dockerfile so a typo in PKG can never build
# and ship a production image under the wrong command by accident — that
# Dockerfile only ever builds from ./cmd/*.
FROM golang:1.25-alpine AS build
ARG PKG
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ${PKG}

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/app /app
USER app
ENTRYPOINT ["/app"]
