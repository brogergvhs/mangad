FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mangad ./main.go

FROM gcr.io/distroless/static-debian12

COPY --from=build /out/mangad /usr/local/bin/mangad
ENV XDG_CONFIG_HOME=/config
VOLUME ["/config", "/data", "/downloads"]
ENTRYPOINT ["/usr/local/bin/mangad"]
