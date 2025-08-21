# Build
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/webhook ./cmd/webhook

# Run
FROM gcr.io/distroless/static:nonroot
WORKDIR /
USER 65532:65532
COPY --from=build /out/webhook /webhook
# cert-manager will mount TLS to /tls
ENTRYPOINT ["/webhook"]
