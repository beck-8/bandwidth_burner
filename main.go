package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
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

func main() {
	startTime := time.Now()
	log.Printf("程序启动，版本: %s-%s", Version, CurrentCommit)

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

	showVersion := flag.Bool("v", false, "Show version")
	concurrency := flag.Int("c", defaultConcurrency, "Number of concurrent downloads")
	timeout := flag.Int("t", defaultTimeout, "Runtime in seconds (0 for no timeout)")
	keepAlives := flag.Bool("k", defaultKeepAlives, "Enable keepAlives")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Version: %s\nCommit: %s\n", Version, CurrentCommit)
		return
	}

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

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DisableKeepAlives:     !*keepAlives,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       time.Second * 90,
			TLSHandshakeTimeout:   time.Second * 30,
			ResponseHeaderTimeout: time.Second * 10,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	var wg sync.WaitGroup
	urlChan := make(chan string)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		elapsed := time.Since(startTime).Seconds()
		totalMB := float64(totalBytes.Load()) / 1024 / 1024
		avgSpeed := 0.0
		if elapsed > 0 {
			avgSpeed = totalMB / elapsed
		}
		log.Printf("收到终止信号 %v，总共消耗流量: %.3f GB，平均速度: %.3f MB/s", sig, totalMB/1024, avgSpeed)
		os.Exit(1)
	}()

	if *timeout > 0 {
		go func() {
			time.Sleep(time.Duration(*timeout) * time.Second)
			elapsed := time.Since(startTime).Seconds()
			totalMB := float64(totalBytes.Load()) / 1024 / 1024
			avgSpeed := 0.0
			if elapsed > 0 {
				avgSpeed = totalMB / elapsed
			}
			log.Printf("超时结束，总共消耗流量: %.3f GB，平均速度: %.3f MB/s", totalMB/1024, avgSpeed)
			os.Exit(0)
		}()
	}

	for i := 0; i < *concurrency; i++ {
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

	req.Header.Set("User-Agent", utils.RandUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		log.Println("请求失败:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("状态码非200, StatusCode: %v, url: %s\n", resp.StatusCode, url)
		return
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
