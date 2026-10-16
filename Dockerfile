# One image for every role: the subcommand chooses (api, api --ui, ui,
# ingestor, token). Static binary on scratch, non-root user.

FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
# Vite writes to ../internal/webui/dist, embedded by the Go build.
RUN mkdir -p ../internal/webui/dist && npm run build

FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/webui/dist ./internal/webui/dist
# Version and commit (internal/version): .git is not in the context, the
# build passes them; the build time is the time of this step.
ARG VERSION=dev
ARG COMMIT=
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X github.com/devleesch001/cauri/internal/version.Tag=${VERSION} -X github.com/devleesch001/cauri/internal/version.Commit=${COMMIT} -X github.com/devleesch001/cauri/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	-o /out/cauri ./cmd/cauri

# What scratch lacks for a static binary, taken from Alpine: CA certificates
# (the ingestor calls the API over HTTPS), time zones (TZ), the nonroot user
# (uid 65532) and its /data directory, /tmp.
FROM alpine:3.22 AS rootfs
RUN apk add --no-cache ca-certificates tzdata \
 && mkdir -p /rootfs/etc/ssl/certs /rootfs/usr/share /rootfs/tmp /data \
 && cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/ \
 && cp -r /usr/share/zoneinfo /rootfs/usr/share/ \
 && echo 'nonroot:x:65532:65532:nonroot:/data:/sbin/nologin' > /rootfs/etc/passwd \
 && echo 'nonroot:x:65532:' > /rootfs/etc/group \
 && chmod 1777 /rootfs/tmp

FROM scratch
COPY --from=rootfs /rootfs/ /
# Owned by the nonroot user so that a new volume mounted on /data is writable
# (created by this COPY only: a directory created before would stay root's).
COPY --from=rootfs --chown=65532:65532 /data /data
COPY --from=build /out/cauri /usr/local/bin/cauri
WORKDIR /data
ENV CAURI_DB_DSN=/data/cauri.db \
    CAURI_TTY_DIR=/data/tty \
    CAURI_STATE_DIR=/data/ingestor
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cauri"]
CMD ["api", "--ui"]
