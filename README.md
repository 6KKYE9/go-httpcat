# go-httpcat

想临时起个服务、查个 IP、探个端口，还要装一堆东西？没必要。

命令行 HTTP 客户端，能把一次请求的各阶段耗时拆开看——慢在 DNS、建连、TLS 握手，还是服务端处理，一眼就知道。

## 安装

```bash
go build -o go-httpcat.exe
```

## 用法

```bash
go-httpcat example.com                          # scheme 可以省，默认 http
go-httpcat -i https://example.com               # 只看响应头
go-httpcat -timing https://example.com          # 看各阶段耗时
go-httpcat -X POST -d "a=1&b=2" example.com/api
go-httpcat -H "Accept: application/json" -H "X-Token: abc" example.com
go-httpcat -k https://self-signed.example.com   # 跳过证书校验
```

`-timing` 的输出大概长这样：

```
DNS 解析     12.3 ms
建立连接     31.7 ms
TLS 握手     48.2 ms
服务端      103.5 ms
内容传输      2.1 ms
总计        198.4 ms
响应体     15234 字节
```

某一阶段显示 `-` 表示没发生：比如复用了连接就没有建连耗时，普通 http 就没有 TLS 握手。

## 参数

| 参数 | 说明 |
|---|---|
| `-X` | 请求方法，默认 GET |
| `-d` | 请求体，给了这个又没写 `-X` 时自动用 POST |
| `-H` | 请求头，可以重复给，写成 `"Key: Value"` |
| `-t` | 超时，默认 10s |
| `-i` | 只打印响应头 |
| `-timing` | 打印各阶段耗时 |
| `-k` | 跳过 TLS 证书校验 |

## 说明

零依赖纯 Go，耗时数据来自标准库的 `net/http/httptrace`。

响应状态码是 4xx/5xx 时进程退出码为 1，可以直接在脚本里判断请求成没成功。

请求头的值里带冒号（比如 Referer 是个完整 URL）不会被截断，只按第一个冒号切分。
