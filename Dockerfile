#############################################
# CIMPLR Inbound Email Service — standalone
# Port 8182, no Postgres, POST-only API
#############################################
FROM golang:1.24-bookworm AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o email-service ./cmd/

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata curl
WORKDIR /app
COPY --from=build /app/email-service /app/email-service
EXPOSE 8182
ENV PORT=8182
CMD ["/app/email-service"]
