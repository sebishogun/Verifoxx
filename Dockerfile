# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27.0-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=devel
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X github.com/sebishogun/verifoxx/internal/buildinfo.version=${VERSION}" \
    -o /out/verifoxx ./cmd/verifoxx

FROM scratch

COPY --from=build --chown=65532:65532 /out/verifoxx /verifoxx
USER 65532:65532
ENTRYPOINT ["/verifoxx"]
CMD ["evaluate"]
