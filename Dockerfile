FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mcp-server ./cmd/mcp-server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/mcp-server /usr/local/bin/mcp-server
COPY configs/config.example.yaml /app/config.yaml
EXPOSE 8080
ENTRYPOINT ["mcp-server", "-config", "/app/config.yaml"]