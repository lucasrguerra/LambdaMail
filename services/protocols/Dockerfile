FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/lambdamail-protocols ./cmd/lambdamail-protocols

FROM alpine:3.20
# libcap provides setcap and is dropped again once the capability is set.
RUN apk add --no-cache ca-certificates libcap

COPY --from=build /out/lambdamail-protocols /app/lambdamail-protocols

# This process terminates SMTP from the open internet, so it must not run as
# root (PLAN.md section 17, risk R5). Every mail port it binds is below 1024,
# which normally requires root - the file capability grants exactly that one
# privilege to this one binary instead.
RUN setcap 'cap_net_bind_service=+ep' /app/lambdamail-protocols \
    && apk del libcap

RUN addgroup -g 10001 -S lambdamail \
    && adduser -u 10001 -S -G lambdamail lambdamail \
    # Created here so a fresh named volume mounted at this path inherits the
    # ownership: Docker seeds an empty volume from the image directory.
    && mkdir -p /var/lambdamail/spool \
    && chown -R lambdamail:lambdamail /var/lambdamail

USER lambdamail:lambdamail

EXPOSE 8080
ENTRYPOINT ["/app/lambdamail-protocols"]
