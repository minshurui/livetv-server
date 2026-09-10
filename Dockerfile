# =====================================================================
# livetv-allinone — 直播全链路 + 电视源自愈 单容器镜像 (优化体积版)
#   自研: Go livetv(35455/19090) + Python 虎牙(35456/19091) + nginx反代(8081)
#   开源: guovern/iptv-api(8080 电视源聚合/自愈)
# 运行时: python:3.14-alpine
# 双架构: docker buildx --platform linux/amd64,linux/arm64 (见 docker/BUILD.md)
# 体积保留可裁剪: ffmpeg 用 --build-arg WITH_FFMPEG=0 可省 ~80MB(无HLS转码/测速截图)
# =====================================================================

# ---------- Stage 1: iptv-api 依赖 + 编译 nginx-rtmp ----------
FROM python:3.14-alpine AS iptv-builder
ARG APP_WORKDIR=/iptv-api
ARG NGINX_VER=1.27.4
ARG RTMP_VER=1.2.2
# 可选构建代理；默认关闭，避免镜像构建依赖个人网络。
ARG BUILD_PROXY=
WORKDIR $APP_WORKDIR
COPY src/iptv-api/Pipfile* ./
RUN if [ -n "$BUILD_PROXY" ]; then \
      export http_proxy=$BUILD_PROXY https_proxy=$BUILD_PROXY HTTP_PROXY=$BUILD_PROXY HTTPS_PROXY=$BUILD_PROXY; \
    fi; \
    n=0; until apk add --no-cache gcc musl-dev python3-dev libffi-dev zlib-dev jpeg-dev wget make pcre-dev openssl-dev curl; do \
      n=$((n + 1)); [ "$n" -ge 3 ] && exit 1; sleep $((n * 5)); \
    done \
  && pip install --no-cache-dir pipenv \
  && PIPENV_VENV_IN_PROJECT=1 pipenv install --deploy \
  && cd $APP_WORKDIR \
  && wget -q https://nginx.org/download/nginx-${NGINX_VER}.tar.gz && tar xzf nginx-${NGINX_VER}.tar.gz \
  && wget -q https://github.com/arut/nginx-rtmp-module/archive/v${RTMP_VER}.tar.gz && tar xzf v${RTMP_VER}.tar.gz \
  && cd $APP_WORKDIR/nginx-${NGINX_VER} \
  && ./configure \
       --add-module=$APP_WORKDIR/nginx-rtmp-module-${RTMP_VER} \
       --conf-path=/etc/nginx/nginx.conf \
       --error-log-path=/var/log/nginx/error.log \
       --http-log-path=/var/log/nginx/access.log \
       --with-cc-opt='-DNGX_HAVE_PWRITE=0 -DNGX_HAVE_PWRITEV=0' \
       --with-http_ssl_module \
  && make -j$(nproc) && make install \
  && cd / && rm -rf $APP_WORKDIR/nginx-${NGINX_VER} $APP_WORKDIR/nginx-rtmp-module-${RTMP_VER} \
             /root/.cache /var/cache/apk/* /tmp/*

# ---------- Stage 2: Go livetv ----------
FROM golang:1.26-alpine AS gobuild
WORKDIR /build
COPY *.go ./
COPY go.mod ./
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /livetv . \
  && rm -rf /root/.cache

# ---------- Stage 3: 运行时 ----------
FROM python:3.14-alpine
ARG APP_WORKDIR=/iptv-api
ARG WITH_FFMPEG=1
ENV APP_WORKDIR=$APP_WORKDIR
ENV APP_PORT=5180
ENV NGINX_HTTP_PORT=8080
ENV NGINX_RTMP_PORT=1935
ENV PUBLIC_PORT=80
ENV PATH="$APP_WORKDIR/.venv/bin:/usr/local/nginx/sbin:$PATH"
ENV PYTHONUNBUFFERED=1
ENV PYTHONIOENCODING=utf-8
ENV DATA=/data

# 运行时系统包(最小集 + 可选 ffmpeg; nginx 用 iptv-api 编译版, 见 ENV PATH)
RUN runtime_packages="pcre curl bash openssh-client" \
 && if [ "$WITH_FFMPEG" = "1" ]; then runtime_packages="$runtime_packages ffmpeg"; fi \
 && n=0 \
 && until apk add --no-cache $runtime_packages; do \
      n=$((n + 1)); [ "$n" -ge 3 ] && exit 1; sleep $((n * 5)); \
    done \
 && mkdir -p /var/log/nginx /run/nginx /etc/nginx/livetv \
 && rm -rf /var/cache/apk/*

# 自研服务
COPY --from=gobuild /livetv /usr/local/bin/livetv
COPY docker/allinone.py /opt/livetv/allinone.py
COPY docker/sync_channels.py /opt/livetv/sync_channels.py
COPY docker/stream-proxy.py /opt/livetv/stream-proxy.py
COPY docker/scripts/ /opt/livetv/scripts/
COPY docker/entry.sh /opt/livetv/entry.sh
COPY docker/nginx/livetv-nginx.conf /etc/nginx/livetv/nginx.conf
RUN chmod +x /opt/livetv/entry.sh /opt/livetv/scripts/*.sh

# 开源 guovern/iptv-api (精细 COPY + 移除冗余, 比官方全量 COPY 更小)
WORKDIR $APP_WORKDIR
COPY src/iptv-api/ $APP_WORKDIR/
COPY --from=iptv-builder $APP_WORKDIR/.venv $APP_WORKDIR/.venv
COPY --from=iptv-builder /usr/local/nginx /usr/local/nginx
COPY src/iptv-api/nginx.conf.template /etc/nginx/nginx.conf.template
# 瘦身: 删非运行必需(docs/tests/桌面UI/win32二进制) + venv/pyc 缓存
RUN rm -rf $APP_WORKDIR/docs $APP_WORKDIR/tests \
       $APP_WORKDIR/desktop_ui $APP_WORKDIR/tkinter_ui \
       $APP_WORKDIR/utils/nginx-rtmp-win32 \
       $APP_WORKDIR/.pytest_cache \
       /root/.cache /var/cache/apk/* \
  && find $APP_WORKDIR -type d -name __pycache__ -prune -delete 2>/dev/null || true \
  && find $APP_WORKDIR -type f -name "*.pyc" -delete 2>/dev/null || true


VOLUME ["/data"]

EXPOSE 35455 19090 35456 19091 8081 8080 1935 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8081/healthz || exit 1

ENTRYPOINT ["/opt/livetv/entry.sh"]
