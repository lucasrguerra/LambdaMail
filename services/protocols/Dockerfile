FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/lambdamail-protocols ./cmd/lambdamail-protocols

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/lambdamail-protocols /app/lambdamail-protocols
EXPOSE 8080
ENTRYPOINT ["/app/lambdamail-protocols"]
