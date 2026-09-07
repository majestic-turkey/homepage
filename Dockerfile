# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build
WORKDIR /src

# No third-party dependencies, so the source copy is the whole story.
COPY go.mod ./
COPY *.go ./
COPY static ./static

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/homepage .

FROM scratch
COPY --from=build /out/homepage /homepage

# Unprivileged: this process only ever needs to read from a socket proxy.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/homepage"]
