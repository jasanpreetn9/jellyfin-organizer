FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/jellyfin-organizer .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && mkdir /data
COPY --from=build /out/jellyfin-organizer /usr/local/bin/jellyfin-organizer
ENV DATA_DIR=/data
EXPOSE 8981
VOLUME ["/data"]
ENTRYPOINT ["jellyfin-organizer"]
