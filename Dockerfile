# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.4
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src/server
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/minute2win-server .

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/minute2win-server /minute2win-server
COPY manifests/ /manifests/
USER 65532:65532
EXPOSE 8080/tcp 69/udp
ENTRYPOINT ["/minute2win-server"]
# Preview only until the operator supplies profiles, assets and a token.
CMD ["-open=false", "-data="]
