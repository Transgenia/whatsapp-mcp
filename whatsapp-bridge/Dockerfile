FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
RUN CGO_ENABLED=1 go test -count=1 ./... && go vet ./... && CGO_ENABLED=1 go build -trimpath -o /out/whatsapp-bridge .

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && mkdir -p /app/store && chown 1000:1000 /app/store
WORKDIR /app
COPY --from=build /out/whatsapp-bridge /app/whatsapp-bridge
USER 1000:1000
ENV WHATSAPP_ALLOW_PAIRING=false
ENV WHATSAPP_ALLOW_SESSION_UPGRADE=false
VOLUME ["/app/store"]
ENTRYPOINT ["/app/whatsapp-bridge"]
