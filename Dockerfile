FROM golang:1.26 AS build

ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X 'github.com/brogergvhs/mangad/cmd.Version=${VERSION}'" -o /out/mangad ./main.go
# Pre-create volume mount points owned by nonroot (65532); distroless has no shell.
RUN mkdir -p /out/config /out/data /out/downloads

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mangad /usr/local/bin/mangad
COPY --from=build --chown=nonroot:nonroot /out/config /config
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY --from=build --chown=nonroot:nonroot /out/downloads /downloads
ENV XDG_CONFIG_HOME=/config
VOLUME ["/config", "/data", "/downloads"]
ENTRYPOINT ["/usr/local/bin/mangad"]
