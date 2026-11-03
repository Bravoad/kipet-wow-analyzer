ARG GO_IMAGE=golang:1.27-alpine
ARG ALPINE_IMAGE=alpine:3.23
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY rules ./rules
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate

FROM ${ALPINE_IMAGE}
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kipet
COPY --from=build /out/ /app/
USER kipet
WORKDIR /app
ENTRYPOINT ["/app/api"]
