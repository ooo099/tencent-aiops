# 构建阶段
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/aiosight ./cmd/aiosight \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/router ./cmd/router

# 运行阶段：静态二进制 + 极小基础镜像
FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/aiosight /aiosight
COPY --from=build /out/router /router
EXPOSE 8080 8081 9000 9001/udp
ENTRYPOINT ["/aiosight"]
