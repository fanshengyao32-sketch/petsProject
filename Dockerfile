FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pet-booking ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache tzdata && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/pet-booking /app/pet-booking
COPY web/templates /app/web/templates
USER app
EXPOSE 8080
ENTRYPOINT ["/app/pet-booking"]

