FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath -o /out/wwfc .
FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/openpak/wfc-race"
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/wwfc /usr/local/bin/wwfc
COPY --chown=app:app config.template.xml entrypoint.sh game_list.tsv motd.txt ./
RUN chmod +x entrypoint.sh && mkdir -p /app/logs /app/state && chown -R app:app /app
USER app
EXPOSE 20208
ENTRYPOINT ["/app/entrypoint.sh"]
