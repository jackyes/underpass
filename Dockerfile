FROM golang:1.23-alpine AS builder

WORKDIR /usr/src/app

COPY . .

RUN go build -o bin/server ./cmd/server

FROM alpine:3.20 AS runner

COPY --from=builder /usr/src/app/bin/server /usr/bin/underpass

ENTRYPOINT [ "underpass" ]
