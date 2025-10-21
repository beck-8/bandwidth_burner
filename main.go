package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/beck-8/bandwidth_burner/utils"
)

var (
	Version       = "dev"
	CurrentCommit = "unknown"
	totalBytes    atomic.Uint64
	lastBytes     atomic.Uint64
)

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

// customDialer: 自定义域名解析
func customDialer(resolveMap map[string]string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if ip, exists := resolveMap[addr]; exists {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			newAddr := net.JoinHostPort(ip, port)
			return dialer.DialContext(ctx, network, newAddr)
		}
		return dialer.DialContext(ctx, network, addr)
	}
}

func main() {
	app := &cli.App{
		Name:    "bandwidth_burner",
		Usage:   "HTTP 并发下载带宽测试工具",
		Version: Version + "-" + CurrentCommit,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "concurrency",
				Aliases: []string{"c"},
				Usage:   "并发请求数",
				Value:   32,
				EnvVars: []string{"CONCURRENCY"},
			},
			&cli.IntFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Usage:   "运行时长 (秒, 0 表示无限制)",
				Value:   0,
				EnvVars: []string{"TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "keepalives",
				Aliases: []string{"k"},
				Usage:   "启用 keep-alive",
				Value:   false,
				EnvVars: []string{"KEEPALIVES"},
			},
			&cli.StringFlag{
				Name:    "user-agent",
				Aliases: []string{"ua"},
				Usage:   "指定 User-Agent，不填则随机",
				EnvVars: []string{"USERAGENT"},
			},
			&cli.StringSliceFlag{
				Name:  "header",
				Usage: "自定义请求头 (格式: 'Key: Value')，可多次指定",
			},
			&cli.StringSliceFlag{
				Name:  "resolve",
				Usage: "自定义解析 (格式: 'host:port:ip')，可多次指定",
			},
			&cli.StringFlag{
				Name:    "file",
				Aliases: []string{"f"},
				Usage:   "指定包含 URL 列表的文件",
				EnvVars: []string{"DOWN_FILE"},
			},
		},
		Action: func(c *cli.Context) error {
			return run(
				c.Int("concurrency"),
				c.Int("timeout"),
				c.Bool("keepalives"),
				c.String("user-agent"),
				parseHeaders(c.StringSlice("header")),
				parseResolve(c.StringSlice("resolve")),
				c.String("file"),
				c.Args().Slice(),
			)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func run(concurrency, timeout int, keepAlives bool, userAgent string,
	headers map[string]string, resolveMap map[string]string,
	fileList string, urls []string) error {

	startTime := time.Now()
	log.Printf("程序启动，版本: %s-%s", Version, CurrentCommit)

	// 如果没传 URL 参数，从文件读取
	if len(urls) == 0 && fileList != "" {
		data, err := os.ReadFile(fileList)
		if err != nil {
			return fmt.Errorf("读取 fileList 失败: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			u := strings.TrimSpace(line)
			if u == "" || strings.HasPrefix(u, "#") {
				continue
			}
			urls = append(urls, u)
		}
	}

	if len(urls) == 0 {
		return fmt.Errorf("请至少提供一个 URL")
	}

	// HTTP Transport
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
	if len(resolveMap) > 0 {
		transport.DialContext = customDialer(resolveMap)
		log.Printf("已配置自定义解析: %v", resolveMap)
	}

	client := &http.Client{Transport: transport}

	// 统计 goroutine
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	calcStats := func() (float64, float64) {
		elapsed := time.Since(startTime).Seconds()
		totalMB := float64(totalBytes.Load()) / 1024 / 1024
		totalGB := totalMB / 1024
		avgSpeed := 0.0
		if elapsed > 0 {
			avgSpeed = totalMB / elapsed
		}
		return totalGB, avgSpeed
	}

	// 定时打印
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		lastBytes.Store(totalBytes.Load())
		lastTime := time.Now()

		for range ticker.C {
			now := time.Now()
			cur := totalBytes.Load()
			diff := cur - lastBytes.Load()
			sec := now.Sub(lastTime).Seconds()
			speed := float64(diff) / 1024 / 1024 / sec
			totalGB, avg := calcStats()
			log.Printf("实时速度: %.3f MB/s | 总流量: %.3f GiB | 平均速度: %.3f MB/s",
				speed, totalGB, avg)
			lastBytes.Store(cur)
			lastTime = now
		}
	}()

	// 信号退出
	go func() {
		sig := <-sigChan
		totalGB, avg := calcStats()
		log.Printf("收到信号 %v，总流量: %.3f GiB，平均速度: %.3f MB/s", sig, totalGB, avg)
		os.Exit(0)
	}()

	// 超时退出
	if timeout > 0 {
		go func() {
			time.Sleep(time.Duration(timeout) * time.Second)
			totalGB, avg := calcStats()
			log.Printf("超时退出，总流量: %.3f GiB，平均速度: %.3f MB/s", totalGB, avg)
			os.Exit(0)
		}()
	}

	// 启动 workers
	var wg sync.WaitGroup
	urlChan := make(chan string)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(r.Intn(10)) * time.Second)
			for u := range urlChan {
				download(client, u, userAgent, headers)
			}
		}()
	}

	// 无限循环
	for {
		for _, u := range urls {
			urlChan <- u
		}
	}
}

func download(client *http.Client, url, userAgent string, headers map[string]string) {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

	countingReader := &CountingReader{reader: resp.Body, count: &totalBytes}
	_, err = io.Copy(io.Discard, countingReader)
	if err != nil {
		log.Println("读取响应失败:", err)
	}
}

func parseHeaders(list []string) map[string]string {
	headers := make(map[string]string)
	for _, h := range list {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return headers
}

func parseResolve(list []string) map[string]string {
	m := make(map[string]string)
	for _, r := range list {
		// 分割为至少三部分（domain:port:ip，ip可能含冒号）
		parts := strings.SplitN(r, ":", 3) // 最多分割为3部分，避免IPv6被拆分过多
		// 校验格式：必须至少3部分，且domain、port、ip均不为空
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			continue // 跳过无效格式
		}
		domain, port, ip := parts[0], parts[1], parts[2]
		// 组合键（domain:port），存入map
		m[domain+":"+port] = ip
	}
	return m
}
