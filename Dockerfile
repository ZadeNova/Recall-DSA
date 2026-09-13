# Multi-stage build. modernc.org/sqlite is pure Go (no CGO), so the final
# image needs nothing beyond the compiled binary itself — no libc, no
# shell, no package manager. time/tzdata is blank-imported in main.go
# specifically so this works: the IANA timezone database is embedded in
# the binary rather than read from /usr/share/zoneinfo, which distroless
# doesn't have.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /recall-server ./cmd/server

FROM gcr.io/distroless/static-debian12

COPY --from=builder /recall-server /recall-server

ENTRYPOINT ["/recall-server"]
