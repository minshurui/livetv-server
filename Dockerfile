# =====================================================================
# livetv-allinone — 直播全链路 + 电视源自愈 单容器镜像
#   自研服务: Go livetv(35455/19090) + Python 虎牙(35456/19091) + nginx(8081)
#   开源工程: guovern/iptv-api (8080 聚合电视源, 自愈)
# 运行时: python:3.14-alpine (满足 iptv-api 的 py3.14, 同时是 Alpine)
# 构建: WSL Ubuntu + buildx 多架构 (amd64/arm64/arm)
# =====================================================================

# ---------- Stage 1: builder (iptv-api 依赖 + 编译 nginx-rtmp) ----------
FROM python:3.14-alpine AS iptv-builder
ARG APP_WORKDIR=/iptv-api
ARG NGINX_VER=1.27.4
ARG RTMP_VER=1.2.2
WORKDIR $APP_WORKDIR
COPY src/iptv-api/Pipfile* ./
RUN apk add --no-cache gcc musl-dev python3-dev libffi-dev zlib-dev jpeg-dev wget make pcre-dev openssl-dev \
  && pip install pipenv \
  && PIPENV_VENV_IN_PROJECT=1 pipenv install --deploy
RUN wget https://nginx.org/download/nginx-${NGINX_VER}.tar.gz && tar xzf nginx-${NGINX_VER}.tar.gz \
  && wget https://github.com/arut/nginx-rtmp-module/archive/v${RTMP_VER}.tar.gz && tar xzf v${RTMP_VER}.tar.gz
WORKDIR $APP_WORKDIR/nginx-${NGINX_VER}
RUN ./configure \
      --add-module=$APP_WORKDIR/nginx-rtmp-module-${RTMP_VER} \
      --conf-path=/etc/nginx/nginx.conf \
      --error-log-path=/var/log/nginx/error.log \
      --http-log-path=/var/log/nginx/access.log \
      --with-cc-opt='-DNGX_HAVE_PWRITE=0 -DNGX_HAVE_PWRITEV=0' \
      --with-http_ssl_module \
  && make && make install

# ---------- Stage 2: 编译自研 Go livetv ----------
FROM golang:1.26-alpine AS gobuild
WORKDIR /build
COPY *.go ./
COPY go.mod ./
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /livetv .

# ---------- Stage 3: 运行时 (python:3.14-alpine) ----------
FROM python:3.14-alpine
ARG APP_WORKDIR=/iptv-api
ENV APP_WORKDIR=$APP_WORKDIR
ENV APP_PORT=5180
ENV NGINX_HTTP_PORT=8080
ENV NGINX_RTMP_PORT=1935
ENV PUBLIC_PORT=80
ENV PATH="$APP_WORKDIR/.venv/bin:/usr/local/nginx/sbin:$PATH"
ENV PYTHONUNBUFFERED=1
ENV PYTHONIOENCODING=utf-8
ENV DATA=/data

# --- 自研服务(Go + Python) ---
COPY --from=gobuild /livetv /usr/local/bin/livetv
COPY docker/allinone.py /opt/livetv/allinone.py
COPY docker/stream-proxy.py /opt/livetv/stream-proxy.py
COPY docker/scripts/ /opt/livetv/scripts/
COPY docker/entry.sh /opt/livetv/entry.sh
COPY docker/nginx/livetv-nginx.conf /etc/nginx/livetv/nginx.conf
RUN chmod +x /opt/livetv/entry.sh /opt/livetv/scripts/*.sh

# --- 开源 guovern/iptv-api ---
WORKDIR $APP_WORKDIR
COPY src/iptv-api/ $APP_WORKDIR/
COPY --from=iptv-builder $APP_WORKDIR/.venv $APP_WORKDIR/.venv
COPY --from=iptv-builder /usr/local/nginx /usr/local/nginx
COPY src/iptv-api/nginx.conf.template /etc/nginx/nginx.conf.template

# apk 装自研反代 nginx + 工具
RUN apk add --no-cache ffmpeg pcre nginx curl bash openssh-client \
  && mkdir -p /var/log/nginx /run/nginx /etc/nginx/livetv \
  && rm -f /etc/nginx/http.d/default.conf 2>/dev/null || true

# 数据卷: 频道 / 电视源 / 日志 (挂载外部)
VOLUME ["/data"]

EXPOSE 35455 19090 35456 19091 8081 8080 1935 80

ENTRYPOINT ["/opt/livetv/entry.sh"]