# 构建阶段
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/aiosight ./cmd/aiosight

# 运行阶段：静态二进制 + 极小基础镜像
FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/aiosight /aiosight
EXPOSE 8080 9000 9001/udp
ENTRYPOINT ["/aiosight"]
