FROM golang:1.26 AS build

ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X 'github.com/brogergvhs/mangad/cmd.Version=${VERSION}'" -o /out/mangad ./main.go

# Runs as root so it can write root-owned Docker named volumes. Distroless has
# no shell to chown mounts at startup; to run non-root, pre-create the volumes
# owned by your uid (or use bind mounts) and add `user:` in compose.
FROM gcr.io/distroless/static-debian12

COPY --from=build /out/mangad /usr/local/bin/mangad
ENV XDG_CONFIG_HOME=/config
VOLUME ["/config", "/data", "/downloads"]
ENTRYPOINT ["/usr/local/bin/mangad"]
