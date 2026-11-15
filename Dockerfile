# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.4
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src/server
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/minute2win-server .
RUN apt-get update && apt-get install -y --no-install-recommends libcap2-bin && rm -rf /var/lib/apt/lists/*

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/minute2win-server /minute2win-server
COPY manifests/ /manifests/
# Keep the existing bind capability across non-root exec. The build-only loader
# and libcap tools are mounted transiently; none enter the runtime filesystem.
# This image build currently targets linux/amd64.
USER 0
RUN --mount=type=bind,from=build,source=/,target=/toolchain,ro ["/toolchain/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2", "--library-path", "/toolchain/usr/lib/x86_64-linux-gnu", "/toolchain/usr/sbin/setcap", "cap_net_bind_service=ep", "/minute2win-server"]
USER 65532:65532
EXPOSE 8080/tcp 69/udp
ENTRYPOINT ["/minute2win-server"]
# Preview only until the operator supplies profiles, assets and a token.
CMD ["-open=false", "-data="]
