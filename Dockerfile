FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
ARG VERSION=v0.1.0-rc.1
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X tadmin/internal/app.Version=${VERSION}" -o /out/tadmin ./cmd/tadmin \
    && mkdir -p /out/data && chmod 0700 /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/tadmin /usr/local/bin/tadmin
COPY --from=build --chown=65532:65532 /out/data /var/lib/tadmin
USER 65532:65532
WORKDIR /var/lib/tadmin
ENTRYPOINT ["/usr/local/bin/tadmin"]
CMD ["--config", "/etc/tadmin/config.toml"]
