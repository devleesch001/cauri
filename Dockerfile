# One image for every role: the subcommand chooses (api, api --ui, ui,
# ingestor, token). Static binary, distroless base, non-root user.

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
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cauri ./cmd/cauri \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/cauri /usr/local/bin/cauri
# Owned by the nonroot user (65532) so that a new volume mounted on /data
# is writable.
COPY --from=build --chown=65532:65532 /out/data /data
WORKDIR /data
ENV CAURI_DB_DSN=/data/cauri.db \
    CAURI_TTY_DIR=/data/tty \
    CAURI_STATE_DIR=/data/ingestor
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cauri"]
CMD ["api", "--ui"]
