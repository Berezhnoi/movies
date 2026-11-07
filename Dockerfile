FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/movies-api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/movies-seed ./cmd/seed

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 app

COPY --from=builder /out/movies-api /out/movies-seed /usr/local/bin/

USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/movies-api"]
