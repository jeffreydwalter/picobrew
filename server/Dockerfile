# syntax=docker/dockerfile:1
ARG HUB_BINARY=server
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS server
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY tools/usb-prepare/ tools/usb-prepare/
RUN target_os="${TARGETOS:-$(go env GOOS)}" \
    && target_arch="${TARGETARCH:-$(go env GOARCH)}" \
    && go run ./tools/usb-prepare -platform "${target_os}-${target_arch}"
RUN target_os="${TARGETOS:-$(go env GOOS)}" \
    && target_arch="${TARGETARCH:-$(go env GOARCH)}" \
    && if [ "${target_arch}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then export GOARM="${TARGETVARIANT#v}"; fi \
    && CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" \
       go build -trimpath -ldflags='-s -w' -o /out/picobrew-hub ./cmd/picobrew-hub

# The release workflow verifies the native Hub download for each platform.
FROM scratch AS released
ARG TARGETARCH
COPY --chmod=0755 release-binaries/picobrew-hub-linux-${TARGETARCH} /out/picobrew-hub

FROM ${HUB_BINARY} AS binary
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 hub && useradd --uid 10001 --gid hub --no-create-home hub \
    && mkdir /data && chown hub:hub /data
COPY --from=binary /out/picobrew-hub /usr/local/bin/picobrew-hub
WORKDIR /app
USER hub
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD curl --fail --silent http://127.0.0.1:8080/healthz > /dev/null || exit 1
ENTRYPOINT ["picobrew-hub"]
CMD ["--listen", "0.0.0.0:8080", "--data", "/data"]
