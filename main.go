// go-httpcat 发一个 HTTP 请求并把各阶段耗时拆开显示，
// 用来判断慢在 DNS、建连、TLS 握手还是服务端处理。
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"sort"
	"strings"
	"time"
)

// Timing 记录一次请求各阶段的耗时。
type Timing struct {
	DNS       time.Duration
	Connect   time.Duration
	TLS       time.Duration
	Server    time.Duration // 从请求发完到收到首字节
	Transfer  time.Duration // 首字节到读完 body
	Total     time.Duration
	StatusMsg string
	Status    int
	BodySize  int64
	Header    http.Header
	Body      []byte
}

// headerFlag 收集重复出现的 -H，形如 -H "Key: Value"。
type headerFlag []string

func (h *headerFlag) String() string { return strings.Join(*h, ", ") }

func (h *headerFlag) Set(v string) error {
	if !strings.Contains(v, ":") {
		return fmt.Errorf("请求头要写成 \"Key: Value\"，收到 %q", v)
	}
	*h = append(*h, v)
	return nil
}

// parseHeaders 把 "Key: Value" 列表转成 http.Header。
// 冒号后的空格可有可无，值里带冒号（比如 URL）不能被截断。
func parseHeaders(raw []string) (http.Header, error) {
	h := http.Header{}
	for _, item := range raw {
		i := strings.Index(item, ":")
		if i <= 0 {
			return nil, fmt.Errorf("请求头格式不对: %q", item)
		}
		k := strings.TrimSpace(item[:i])
		v := strings.TrimSpace(item[i+1:])
		if k == "" {
			return nil, fmt.Errorf("请求头缺少字段名: %q", item)
		}
		h.Add(k, v)
	}
	return h, nil
}

// normalizeURL 允许省略 scheme，缺省按 http 处理。
func normalizeURL(raw string) string {
	if raw == "" {
		return raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "http://" + raw
}

// fetch 发请求并采集各阶段耗时。insecure 为真时跳过证书校验。
func fetch(method, rawURL string, hdr http.Header, body string, timeout time.Duration, insecure bool) (*Timing, error) {
	req, err := http.NewRequest(method, rawURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	t := &Timing{}
	var start, dnsStart, connStart, tlsStart, wroteAt time.Time

	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			if !dnsStart.IsZero() {
				t.DNS = time.Since(dnsStart)
			}
		},
		ConnectStart: func(_, _ string) { connStart = time.Now() },
		ConnectDone: func(_, _ string, _ error) {
			if !connStart.IsZero() {
				t.Connect = time.Since(connStart)
			}
		},
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			if !tlsStart.IsZero() {
				t.TLS = time.Since(tlsStart)
			}
		},
		WroteRequest: func(httptrace.WroteRequestInfo) { wroteAt = time.Now() },
		GotFirstResponseByte: func() {
			if !wroteAt.IsZero() {
				t.Server = time.Since(wroteAt)
			}
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Timeout: timeout, Transport: tr}

	start = time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	firstByteAt := time.Now()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	t.Transfer = time.Since(firstByteAt)
	t.Total = time.Since(start)
	t.Status = resp.StatusCode
	t.StatusMsg = resp.Status
	t.Header = resp.Header
	t.Body = data
	t.BodySize = int64(len(data))
	return t, nil
}

// ms 把耗时格式化成毫秒，0 表示这一阶段没发生（比如复用连接、非 HTTPS）。
func ms(d time.Duration) string {
	if d == 0 {
		return "     -"
	}
	return fmt.Sprintf("%6.1f", float64(d.Microseconds())/1000)
}

func printTiming(w io.Writer, t *Timing) {
	fmt.Fprintf(w, "DNS 解析   %s ms\n", ms(t.DNS))
	fmt.Fprintf(w, "建立连接   %s ms\n", ms(t.Connect))
	fmt.Fprintf(w, "TLS 握手   %s ms\n", ms(t.TLS))
	fmt.Fprintf(w, "服务端     %s ms\n", ms(t.Server))
	fmt.Fprintf(w, "内容传输   %s ms\n", ms(t.Transfer))
	fmt.Fprintf(w, "总计       %s ms\n", ms(t.Total))
}

func printHeaders(w io.Writer, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(w, "%s: %s\n", k, v)
		}
	}
}

func main() {
	method := flag.String("X", "GET", "请求方法")
	var hdrs headerFlag
	flag.Var(&hdrs, "H", "请求头，可重复，写成 \"Key: Value\"")
	body := flag.String("d", "", "请求体，给了这个且没指定 -X 时自动用 POST")
	timeout := flag.Duration("t", 10*time.Second, "超时时间")
	showTiming := flag.Bool("timing", false, "显示各阶段耗时")
	onlyHead := flag.Bool("i", false, "只显示响应头，不打印 body")
	insecure := flag.Bool("k", false, "跳过 TLS 证书校验")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法: go-httpcat [选项] <URL>")
		flag.PrintDefaults()
		os.Exit(2)
	}

	// 给了 -d 却没显式指定方法，按 curl 的习惯默认改成 POST
	m := *method
	if *body != "" && !isMethodSet() {
		m = http.MethodPost
	}

	hdr, err := parseHeaders(hdrs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	t, err := fetch(m, normalizeURL(flag.Arg(0)), hdr, *body, *timeout, *insecure)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求失败:", err)
		os.Exit(1)
	}

	if *onlyHead || *showTiming {
		fmt.Println(t.StatusMsg)
		printHeaders(os.Stdout, t.Header)
		fmt.Println()
	}
	if *showTiming {
		printTiming(os.Stdout, t)
		fmt.Printf("响应体     %d 字节\n", t.BodySize)
	}
	if !*onlyHead && !*showTiming {
		os.Stdout.Write(t.Body)
	}

	// 4xx/5xx 用非零退出码，方便脚本里直接判断
	if t.Status >= 400 {
		os.Exit(1)
	}
}

// isMethodSet 判断用户有没有显式写 -X。
func isMethodSet() bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "X" {
			set = true
		}
	})
	return set
}
