# Stage 1: Build binary backend Go
FROM golang:alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Salin source code
COPY . .

# Target stage untuk pengetesan
FROM builder AS test
CMD ["go", "test", "-v", "./..."]

# Build binary secara statis
FROM builder AS build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server app/main.go

# Stage 2: Runtime image minimal
FROM alpine:latest

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

COPY --from=build /app/server /app/server

ENV PORT=9000
EXPOSE 9000

CMD ["/app/server"]
