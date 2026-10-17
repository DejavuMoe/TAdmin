FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X tadmin/internal/app.Version=${VERSION}" -o /out/tadmin ./cmd/tadmin \
    && mkdir -p /out/data && chmod 0700 /out/data

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/DejavuMoe/TAdmin" \
    org.opencontainers.image.licenses="MIT"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/tadmin /usr/local/bin/tadmin
COPY LICENSE THIRD_PARTY_NOTICES.txt /usr/share/licenses/tadmin/
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
WORKDIR /data
ENV TADMIN_DATA_DIR=/data \
    TADMIN_LISTEN=0.0.0.0:8080 \
    TADMIN_CONFIG=/etc/tadmin/config.toml
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/usr/local/bin/tadmin", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/tadmin"]
CMD ["run"]
