ARG OCB_VERSION=v0.150.0
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-bookworm AS builder

ARG OCB_VERSION
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
WORKDIR /src
COPY ocb-config.yaml ./
RUN go install go.opentelemetry.io/collector/cmd/builder@${OCB_VERSION} \
    && /go/bin/builder --config ocb-config.yaml \
    && test -x /src/dist/fabric-relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /src/dist/fabric-relay /fabric-relay
USER 65532:65532
EXPOSE 4317 4318 8888 13133
ENTRYPOINT ["/fabric-relay"]
CMD ["--config=/etc/fabric-relay/config.yaml"]
