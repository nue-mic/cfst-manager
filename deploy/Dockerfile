# ---------- 构建阶段 ----------
FROM golang:1.25-alpine AS build
WORKDIR /src
# 前端已预构建并随源码内嵌（web/dist），无需 Node。
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/nue-mic/cfst-manager/internal/version.Number=${VERSION} -X github.com/nue-mic/cfst-manager/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/cfstmgrd ./cmd/cfstmgrd

# ---------- 运行阶段 ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 cfst
WORKDIR /app
COPY --from=build /out/cfstmgrd /usr/local/bin/cfstmgrd
ENV CFST_HTTP_ADDR=:18123 \
    CFST_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 18123
USER cfst
HEALTHCHECK --interval=30s --timeout=4s --start-period=5s --retries=3 \
  CMD ["cfstmgrd", "health", "http://127.0.0.1:18123"]
ENTRYPOINT ["cfstmgrd"]
CMD ["serve"]
