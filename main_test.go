package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseHeaders(t *testing.T) {
	h, err := parseHeaders([]string{"Accept: application/json", "X-Token:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("Accept") != "application/json" {
		t.Fatalf("Accept 解析错: %q", h.Get("Accept"))
	}
	// 冒号后没空格也要能认
	if h.Get("X-Token") != "abc" {
		t.Fatalf("X-Token 解析错: %q", h.Get("X-Token"))
	}
}

// 值里带冒号（比如 Referer 是个 URL）不能被截断
func TestParseHeadersValueWithColon(t *testing.T) {
	h, err := parseHeaders([]string{"Referer: https://a.com:8080/x"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("Referer") != "https://a.com:8080/x" {
		t.Fatalf("值被截断了: %q", h.Get("Referer"))
	}
}

func TestParseHeadersBad(t *testing.T) {
	if _, err := parseHeaders([]string{"没有冒号"}); err == nil {
		t.Fatal("缺冒号应报错")
	}
	if _, err := parseHeaders([]string{": 空字段名"}); err == nil {
		t.Fatal("空字段名应报错")
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"example.com":         "http://example.com",
		"http://example.com":  "http://example.com",
		"https://example.com": "https://example.com",
		"":                    "",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Fatalf("normalizeURL(%q)=%q 想要 %q", in, got, want)
		}
	}
}

func TestFetchBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Method", r.Method)
		w.WriteHeader(201)
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	got, err := fetch("GET", srv.URL, http.Header{}, "", 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != 201 {
		t.Fatalf("状态码应为 201，实际 %d", got.Status)
	}
	if string(got.Body) != "hello" {
		t.Fatalf("body 不对: %q", got.Body)
	}
	if got.BodySize != 5 {
		t.Fatalf("body 长度应为 5，实际 %d", got.BodySize)
	}
	if got.Total <= 0 {
		t.Fatal("总耗时应大于 0")
	}
}

func TestFetchSendsHeaderAndBody(t *testing.T) {
	var gotToken, gotMethod, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Token")
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
	}))
	defer srv.Close()

	h := http.Header{}
	h.Set("X-Token", "t1")
	if _, err := fetch("POST", srv.URL, h, "a=1", 5*time.Second, false); err != nil {
		t.Fatal(err)
	}
	if gotToken != "t1" {
		t.Fatalf("请求头没送到: %q", gotToken)
	}
	if gotMethod != "POST" {
		t.Fatalf("方法不对: %q", gotMethod)
	}
	if gotBody != "a=1" {
		t.Fatalf("请求体不对: %q", gotBody)
	}
	// 带 body 又没写 Content-Type 时应自动补一个
	if gotCT == "" {
		t.Fatal("应自动补上 Content-Type")
	}
}

// 显式给了 Content-Type 就别被覆盖
func TestFetchKeepsExplicitContentType(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	h := http.Header{}
	h.Set("Content-Type", "application/json")
	if _, err := fetch("POST", srv.URL, h, `{"a":1}`, 5*time.Second, false); err != nil {
		t.Fatal(err)
	}
	if gotCT != "application/json" {
		t.Fatalf("显式 Content-Type 被覆盖了: %q", gotCT)
	}
}

func TestMs(t *testing.T) {
	// 0 表示这一阶段没发生，显示成横杠而不是 0.0
	if !strings.Contains(ms(0), "-") {
		t.Fatalf("零值应显示为横杠，实际 %q", ms(0))
	}
	if strings.Contains(ms(1500*time.Millisecond), "-") {
		t.Fatalf("非零值不该是横杠: %q", ms(1500*time.Millisecond))
	}
}

func TestPrintHeadersSorted(t *testing.T) {
	h := http.Header{}
	h.Set("Zeta", "1")
	h.Set("Alpha", "2")
	var buf bytes.Buffer
	printHeaders(&buf, h)
	out := buf.String()
	if strings.Index(out, "Alpha") > strings.Index(out, "Zeta") {
		t.Fatalf("响应头应按名字排序，实际:\n%s", out)
	}
}

func TestPrintTimingHasAllStages(t *testing.T) {
	var buf bytes.Buffer
	printTiming(&buf, &Timing{Total: time.Second})
	for _, want := range []string{"DNS 解析", "建立连接", "TLS 握手", "服务端", "内容传输", "总计"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("耗时输出缺少 %q:\n%s", want, buf.String())
		}
	}
}
