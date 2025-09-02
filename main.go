package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/beck-8/bandwidth_burner/utils"
)

var (
	Version       string = "dev"
	CurrentCommit string = "unknown"
	totalBytes    atomic.Uint64
	lastBytes     atomic.Uint64
	concurrency   int
	timeout       int
	keepAlives    bool
	userAgent     string
	customHeaders headersFlag
	resolveMap    resolveFlag
)

type headersFlag struct {
	m map[string]string
}

func (h *headersFlag) String() string {
	var parts []string
	for k, v := range h.m {
		parts = append(parts, fmt.Sprintf("%s: %s", k, v))
	}
	return strings.Join(parts, ", ")
}

func (h *headersFlag) Set(s string) error {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid header format: %s (expected 'Key: Value')", s)
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	h.m[key] = value
	return nil
}

// resolveFlag 实现自定义域名解析映射
type resolveFlag struct {
	m map[string]string // host:port -> ip
}

func (r *resolveFlag) String() string {
	var parts []string
	for k, v := range r.m {
		parts = append(parts, fmt.Sprintf("%s:%s", k, v))
	}
	return strings.Join(parts, ", ")
}

func (r *resolveFlag) Set(s string) error {
	// 支持格式: domain:port:ip 或 domain::ip (默认端口80)
	parts := strings.Split(s, ":")
	if len(parts) < 3 {
		return fmt.Errorf("invalid resolve format: %s (expected 'domain:port:ip' or 'domain::ip')", s)
	}

	domain := parts[0]
	port := parts[1]
	ip := strings.Join(parts[2:], ":") // 支持IPv6地址

	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	// 验证IP地址格式
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	// 如果端口为空，默认使用80
	if port == "" {
		port = "80"
	}

	key := fmt.Sprintf("%s:%s", domain, port)
	r.m[key] = ip
	return nil
}

// customDialer 实现自定义域名解析的拨号器
func customDialer(resolveMap map[string]string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// 检查是否有自定义解析
		if ip, exists := resolveMap[addr]; exists {
			// 替换地址中的主机名为指定的IP
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			newAddr := net.JoinHostPort(ip, port)
			// log.Printf("解析 %s -> %s", addr, newAddr)
			return dialer.DialContext(ctx, network, newAddr)
		}

		// 使用默认解析
		return dialer.DialContext(ctx, network, addr)
	}
}

type CountingReader struct {
	reader io.ReadCloser
	count  *atomic.Uint64
}

func (cr *CountingReader) Read(p []byte) (n int, err error) {
	n, err = cr.reader.Read(p)
	if n > 0 {
		cr.count.Add(uint64(n))
	}
	return
}

func (cr *CountingReader) Close() error {
	return cr.reader.Close()
}

func init() {
	defaultConcurrency := 32
	if envConcurrency := os.Getenv("CONCURRENCY"); envConcurrency != "" {
		if val, err := strconv.Atoi(envConcurrency); err == nil && val > 0 {
			defaultConcurrency = val
		}
	}

	defaultTimeout := 0
	if envTimeout := os.Getenv("TIMEOUT"); envTimeout != "" {
		if val, err := strconv.Atoi(envTimeout); err == nil && val >= 0 {
			defaultTimeout = val
		}
	}
	defaultKeepAlives := false
	if envKeepAlives := os.Getenv("KeepAlives"); envKeepAlives != "" {
		defaultKeepAlives = true
	}
	defaultUserAgent := ""
	if envUserAgent := os.Getenv("UserAgent"); envUserAgent != "" {
		defaultUserAgent = envUserAgent
	}

	flag.IntVar(&concurrency, "c", defaultConcurrency, "Number of concurrent downloads")
	flag.IntVar(&timeout, "t", defaultTimeout, "Runtime in seconds (0 for no timeout)")
	flag.BoolVar(&keepAlives, "k", defaultKeepAlives, "Enable keepAlives")
	flag.StringVar(&userAgent, "ua", defaultUserAgent, "Specify UserAgent, and do not specify it will be random")
	showVersion := flag.Bool("v", false, "Show version")
	customHeaders.m = make(map[string]string)
	flag.Var(&customHeaders, "h", "Add a custom header in 'Key: Value' format (can be specified multiple times)")
	resolveMap.m = make(map[string]string)
	flag.Var(&resolveMap, "resolve", "Force resolve HOST:PORT to IP (can be specified multiple times, format: 'host:port:ip' or 'host::ip')")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Version: %s\nCommit: %s\n", Version, CurrentCommit)
		os.Exit(0)
	}
}
func main() {
	startTime := time.Now()
	log.Printf("程序启动，版本: %s-%s", Version, CurrentCommit)

	var urls []string
	if flag.NArg() > 0 {
		urls = flag.Args()
	} else {
		if f := os.Getenv("DOWN_FILE"); f != "" {
			file, err := os.Open(f)
			if err != nil {
				log.Fatalln(err)
			}
			body, err := io.ReadAll(file)
			if err != nil {
				log.Fatalln(err)
			}
			for _, u := range strings.Split(string(body), "\n") {
				u := strings.TrimSpace(u)
				if strings.HasPrefix(u, "#") || u == "" {
					continue
				}
				urls = append(urls, u)
			}
		}
	}

	if len(urls) == 0 {
		log.Fatalln("请提供至少一个URL")
		return
	}

	// 创建HTTP传输配置
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DisableKeepAlives:     !keepAlives,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       time.Second * 90,
		TLSHandshakeTimeout:   time.Second * 30,
		ResponseHeaderTimeout: time.Second * 10,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// 如果有自定义域名解析，使用自定义拨号器
	if len(resolveMap.m) > 0 {
		transport.DialContext = customDialer(resolveMap.m)
		log.Printf("已配置自定义域名解析: %v", resolveMap.m)
	}

	client := &http.Client{
		Transport: transport,
	}

	var wg sync.WaitGroup
	urlChan := make(chan string)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	calculateStats := func() (totalGB float64, avgSpeed float64) {
		elapsed := time.Since(startTime).Seconds()
		totalMB := float64(totalBytes.Load()) / 1024 / 1024
		totalGB = totalMB / 1024
		avgSpeed = 0.0
		if elapsed > 0 {
			avgSpeed = totalMB / elapsed
		}
		return totalGB, avgSpeed
	}

	// 30秒定时输出，包含实时速度和平均速度
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		lastBytes.Store(totalBytes.Load())
		lastOutputTime := time.Now()

		for {
			<-ticker.C
			currentTime := time.Now()
			currentBytes := totalBytes.Load()
			lastBytesValue := lastBytes.Load()

			// 计算30秒内的实时速度
			bytesInPeriod := currentBytes - lastBytesValue
			elapsedSeconds := currentTime.Sub(lastOutputTime).Seconds()
			realtimeSpeedMB := float64(bytesInPeriod) / 1024 / 1024 / elapsedSeconds

			// 计算总统计
			totalGB, avgSpeed := calculateStats()

			log.Printf("实时速度: %.3f MB/s | 总流量: %.3f GiB | 平均速度: %.3f MB/s",
				realtimeSpeedMB, totalGB, avgSpeed)

			// 更新记录
			lastBytes.Store(currentBytes)
			lastOutputTime = currentTime
		}
	}()
	go func() {
		sig := <-sigChan
		totalGB, avgSpeed := calculateStats()
		log.Printf("收到终止信号 %v，总共消耗流量: %.3f GiB，平均速度: %.3f MB/s", sig, totalGB, avgSpeed)
		os.Exit(1)
	}()

	if timeout > 0 {
		go func() {
			time.Sleep(time.Duration(timeout) * time.Second)
			totalGB, avgSpeed := calculateStats()
			log.Printf("超时结束，总共消耗流量: %.3f GiB，平均速度: %.3f MB/s", totalGB, avgSpeed)
			os.Exit(0)
		}()
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for url := range urlChan {
				download(client, url)
			}
		}()
	}

	for {
		for _, url := range urls {
			urlChan <- url
		}
	}
	// wg.Wait()
}

func download(client *http.Client, url string) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("创建请求失败:", err)
		return
	}

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	} else {
		req.Header.Set("User-Agent", utils.RandUserAgent())
	}

	for key, value := range customHeaders.m {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Println("请求失败:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("状态码非200, StatusCode: %v, url: %s\n", resp.StatusCode, url)
	}

	countingReader := &CountingReader{
		reader: resp.Body,
		count:  &totalBytes,
	}

	_, err = io.Copy(io.Discard, countingReader)
	if err != nil {
		log.Println("读取响应失败:", err)
		return
	}
}
