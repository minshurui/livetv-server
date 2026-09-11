# syntax=docker/dockerfile:1.7

# 上游镜像固定到多架构 manifest digest，避免每次构建悄悄换依赖。
# 更新方法见 docker/BUILD.md；也可在构建时用 --build-arg IPTV_API_IMAGE=... 覆盖。
ARG IPTV_API_IMAGE=guovern/iptv-api@sha256:72f663dc7e47c9f79737b9f289072d77ab1c3850a46cace9be09343b0060f4e8

# ---------- Stage 1: Go livetv ----------
FROM golang:1.26-alpine AS gobuild
WORKDIR /build
COPY go.mod ./
COPY *.go ./
ENV CGO_ENABLED=0
RUN go test ./... \
 && go build -trimpath -ldflags "-s -w" -o /out/livetv .

# ---------- Stage 2: iptv-api + 本项目服务 ----------
# 直接复用上游已经验证过的 amd64/arm64 运行镜像，避免在 QEMU 下重复编译
# nginx-rtmp；这也是此前 buildx 长时间卡住和 apk 下载失败的主要修复点。
FROM ${IPTV_API_IMAGE}

ARG VCS_REF=unknown
LABEL org.opencontainers.image.source="https://github.com/minshurui/livetv-server" \
      org.opencontainers.image.revision="$VCS_REF" \
      org.opencontainers.image.description="Live TV aggregation and self-healing source service"

ENV DATA=/data \
    APP_WORKDIR=/iptv-api \
    APP_PORT=5180 \
    NGINX_HTTP_PORT=8080 \
    NGINX_RTMP_PORT=1935 \
    LIVETV_HTTP_PORT=8081 \
    AIO_PORT=35455 \
    PROXY_PORT=19090 \
    RES_PORT=35456 \
    PY_PORT=19091 \
    PUBLIC_AIO_PORT=35455 \
    PUBLIC_PROXY_PORT=19090 \
    PUBLIC_PY_PORT=19091 \
    HUYA_CDN=AL \
    HUYA_CODEC=264 \
    IPTV_MIN_CHANNELS=20 \
    IPTV_REJECT_VOD=1 \
    IPTV_BLOCKLIST="" \
    PYTHONUNBUFFERED=1 \
    PYTHONIOENCODING=utf-8

USER root

# curl: 拉流代理和健康检查；bash/openssh-client: 可选 NAS 同步。
RUN runtime_packages="curl bash openssh-client" \
 && n=0 \
 && until apk add --no-cache $runtime_packages; do \
      n=$((n + 1)); [ "$n" -ge 3 ] && exit 1; sleep $((n * 5)); \
    done \
 && mkdir -p /opt/livetv /etc/nginx/livetv /data \
 && rm -rf /var/cache/apk/*

COPY --from=gobuild /out/livetv /usr/local/bin/livetv
COPY docker/allinone.py /opt/livetv/allinone.py
COPY docker/sync_channels.py /opt/livetv/sync_channels.py
COPY docker/filter_iptv.py /opt/livetv/filter_iptv.py
COPY docker/stream-proxy.py /opt/livetv/stream-proxy.py
COPY docker/scripts/ /opt/livetv/scripts/
COPY docker/entry.sh /opt/livetv/entry.sh
COPY docker/healthcheck.sh /opt/livetv/healthcheck.sh
COPY docker/nginx/livetv-nginx.conf /etc/nginx/livetv/nginx.conf.template

RUN chmod +x /opt/livetv/entry.sh /opt/livetv/healthcheck.sh /opt/livetv/scripts/*.sh

VOLUME ["/data"]

EXPOSE 35455 19090 35456 19091 8081 8080 1935

HEALTHCHECK --interval=30s --timeout=10s --start-period=90s --retries=3 \
  CMD ["/opt/livetv/healthcheck.sh"]

ENTRYPOINT ["/opt/livetv/entry.sh"]
