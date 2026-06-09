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
	Version             = "dev"
	CurrentCommit       = "unknown"
	totalBytes          atomic.Uint64
	consecutiveFailures atomic.Int64
)

// CountingConn 包装 net.Conn，统计真实的TCP层面的读写字节数
type CountingConn struct {
	net.Conn
	readCount *atomic.Uint64
}

func (cc *CountingConn) Read(b []byte) (n int, err error) {
	n, err = cc.Conn.Read(b)
	if n > 0 {
		cc.readCount.Add(uint64(n))
	}
	return
}

func (cc *CountingConn) Write(b []byte) (n int, err error) {
	n, err = cc.Conn.Write(b)
	if n > 0 {
		cc.readCount.Add(uint64(n))
	}
	return
}

// customDialer: 自定义域名解析，并在TCP层面统计流量
func customDialer(resolveMap map[string]string, counter *atomic.Uint64) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var conn net.Conn
		var err error
		if ip, exists := resolveMap[addr]; exists {
			var port string
			_, port, err = net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			newAddr := net.JoinHostPort(ip, port)
			conn, err = dialer.DialContext(ctx, network, newAddr)
		} else {
			conn, err = dialer.DialContext(ctx, network, addr)
		}

		if err != nil {
			return nil, err
		}

		// 包装成 CountingConn 以统计真实网络流量
		return &CountingConn{Conn: conn, readCount: counter}, nil
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
			&cli.IntFlag{
				Name:    "request-timeout",
				Usage:   "单次请求超时 (秒, 0 表示无限制)",
				Value:   0,
				EnvVars: []string{"REQUEST_TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "keepalives",
				Aliases: []string{"k"},
				Usage:   "启用 keep-alive",
				Value:   true,
				EnvVars: []string{"KEEPALIVES"},
			},
			&cli.StringFlag{
				Name:    "user-agent",
				Aliases: []string{"ua"},
				Usage:   "指定 User-Agent，不填则随机",
				EnvVars: []string{"USERAGENT"},
			},
			&cli.BoolFlag{
				Name:  "insecure",
				Usage: "跳过 TLS 证书校验 (仅用于测试)",
				Value: false,
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
			&cli.Float64Flag{
				Name:    "max-traffic",
				Aliases: []string{"l"},
				Usage:   "达到指定流量后停止 (单位 GiB, 0 表示无限制)",
				Value:   0,
				EnvVars: []string{"MAX_TRAFFIC"},
			},
			&cli.IntFlag{
				Name:    "max-failures",
				Usage:   "连续失败达到该次数后退出 (目标 down/不可用时避免无限重试, 0 表示禁用)",
				Value:   0,
				EnvVars: []string{"MAX_FAILURES"},
			},
		},
		Action: func(c *cli.Context) error {
			return run(
				c.Int("concurrency"),
				c.Int("timeout"),
				c.Int("request-timeout"),
				c.Bool("keepalives"),
				c.String("user-agent"),
				parseHeaders(c.StringSlice("header")),
				parseResolve(c.StringSlice("resolve")),
				c.Bool("insecure"),
				c.String("file"),
				c.Float64("max-traffic"),
				c.Int("max-failures"),
				c.Args().Slice(),
			)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func run(concurrency, timeout, requestTimeout int, keepAlives bool, userAgent string,
	headers map[string]string, resolveMap map[string]string,
	insecure bool, fileList string, maxTraffic float64, maxFailures int, urls []string) error {

	if concurrency <= 0 {
		return fmt.Errorf("concurrency 必须大于 0")
	}

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
			InsecureSkipVerify: insecure,
		},
		// 始终使用自定义 DialContext 以统计真实网络流量
		DialContext: customDialer(resolveMap, &totalBytes),
	}
	if len(resolveMap) > 0 {
		log.Printf("已配置自定义解析: %v", resolveMap)
	}

	client := &http.Client{Transport: transport}

	// 统计 goroutine
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		var lastBytes uint64
		lastTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				cur := totalBytes.Load()
				elapsed := now.Sub(lastTime).Seconds()
				speed := 0.0
				if elapsed > 0 {
					speed = float64(cur-lastBytes) / 1024 / 1024 / elapsed
				}
				lastBytes = cur
				lastTime = now
				totalGB, avg := calcStats()
				log.Printf("实时速度: %.3f MB/s | 总流量: %.3f GiB | 平均速度: %.3f MB/s",
					speed, totalGB, avg)
			}
		}
	}()

	// 信号退出
	go func() {
		sig := <-sigChan
		totalGB, avg := calcStats()
		log.Printf("收到信号 %v，总流量: %.3f GiB，平均速度: %.3f MB/s", sig, totalGB, avg)
		cancel()
	}()

	// 超时退出
	if timeout > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeout) * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				totalGB, avg := calcStats()
				log.Printf("超时退出，总流量: %.3f GiB，平均速度: %.3f MB/s", totalGB, avg)
				cancel()
			}
		}()
	}

	// 流量上限退出
	if maxTraffic > 0 {
		limitBytes := uint64(maxTraffic * 1024 * 1024 * 1024)
		log.Printf("已设置流量上限: %.3f GiB", maxTraffic)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if totalBytes.Load() >= limitBytes {
						totalGB, avg := calcStats()
						log.Printf("达到流量上限 %.3f GiB，退出。总流量: %.3f GiB，平均速度: %.3f MB/s", maxTraffic, totalGB, avg)
						cancel()
						return
					}
				}
			}
		}()
	}

	// 连续失败熔断退出 (目标 down / 不可用时避免无限重试)
	if maxFailures > 0 {
		log.Printf("已设置连续失败上限: %d 次", maxFailures)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if consecutiveFailures.Load() >= int64(maxFailures) {
						totalGB, avg := calcStats()
						log.Printf("连续失败达到 %d 次，目标可能不可用，退出。总流量: %.3f GiB，平均速度: %.3f MB/s", maxFailures, totalGB, avg)
						cancel()
						return
					}
				}
			}
		}()
	}

	// 启动 workers
	var wg sync.WaitGroup
	urlChan := make(chan string, concurrency*2)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(r.Intn(10)) * time.Second)
			failStreak := 0
			for {
				select {
				case <-ctx.Done():
					return
				case u, ok := <-urlChan:
					if !ok {
						return
					}
					if download(ctx, client, u, userAgent, headers, requestTimeout) {
						failStreak = 0
						consecutiveFailures.Store(0)
						continue
					}
					// 失败后指数退避，避免目标 down 时空转打满 CPU / 刷屏日志
					failStreak++
					consecutiveFailures.Add(1)
					backoff := time.Duration(failStreak) * 200 * time.Millisecond
					if backoff > 5*time.Second {
						backoff = 5 * time.Second
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
					}
				}
			}
		}()
	}

	// URL 生产
	go func() {
		defer close(urlChan)
		for {
			for _, u := range urls {
				select {
				case <-ctx.Done():
					return
				case urlChan <- u:
				}
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
	return nil
}

// download 返回 true 表示本次请求成功；返回 false 表示失败 (用于触发退避与熔断)。
func download(parent context.Context, client *http.Client, url, userAgent string, headers map[string]string, requestTimeout int) bool {
	ctx := parent
	var cancel context.CancelFunc
	if requestTimeout > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(requestTimeout)*time.Second)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Println("创建请求失败:", err)
		return false
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
		// 父 context 被取消属于正常退出，不计为失败
		if parent.Err() != nil {
			return false
		}
		log.Println("请求失败:", err)
		return false
	}
	defer resp.Body.Close()

	// 非 2xx (如 403/404/5xx) 拿不到正常流量，视为失败：
	// 先排空响应体以便复用连接 (keep-alive)，再返回 false 触发退避与熔断。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("状态码非2xx, StatusCode: %v, url: %s\n", resp.StatusCode, url)
		io.Copy(io.Discard, resp.Body)
		return false
	}

	// 流量统计已在 TCP 层面的 CountingConn 中完成，这里只需读取并丢弃数据
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		if parent.Err() != nil {
			return false
		}
		log.Println("读取响应失败:", err)
		return false
	}
	return true
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
