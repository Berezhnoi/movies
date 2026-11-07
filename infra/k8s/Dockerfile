# Lab image: API + seed binaries. Copies only source directories, so local
# secret files (.env, infra/secrets.env) never enter the build context layers.
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY docs ./docs
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/movies-api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/movies-seed ./cmd/seed

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 app
COPY --from=builder /out/movies-api /out/movies-seed /usr/local/bin/
USER 10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/movies-api"]
