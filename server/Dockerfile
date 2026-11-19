FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /denundike ./cmd/denundike

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /denundike .
VOLUME /data
EXPOSE 8080
CMD ["./denundike"]
