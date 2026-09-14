FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM build AS test
RUN apk add --no-cache gcc musl-dev
CMD ["go", "test", "-race", "./..."]

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/server /out/migrate /app/
RUN addgroup -S app && adduser -S -G app app && mkdir -p /data/uploads && chown -R app:app /data
USER app
EXPOSE 8080 6060
CMD ["/app/server"]
