# Build from the repository root: docker build -f build/Dockerfile -t network-controller .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /out/network-controller ./cmd/network-controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/network-controller /usr/local/bin/network-controller
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/network-controller"]
