ARG GO_VERSION=1.25.3

FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=${VERSION}" -o mcp ./cmd/mcp

FROM alpine:latest

RUN apk --no-cache add ca-certificates && adduser -D -u 65532 app

WORKDIR /app
COPY --from=builder /app/mcp .

USER 65532
EXPOSE 8080
CMD ["./mcp"]
