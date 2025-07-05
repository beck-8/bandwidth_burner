# BandwidthBurner
带宽消耗下行流量

一个用于带宽压力测试的工具，支持并发下载、流量统计、自动退出等功能。

## 🚀 功能特性

- ✅ 支持多个 URL 并发下载
- ✅ 支持总流量统计和平均速度输出
- ✅ 支持超时退出
- ✅ 支持中断信号（Ctrl+C）优雅退出
- ✅ 支持 Docker 运行

## 📦二进制运行示例
```bash
./bandwidth_burner -c 16 -t 60 https://example.com/file1 https://example.com/file2
```
参数说明：

- -c: 并发线程数，默认 32
- -t: 运行时间（单位秒），0 表示无限运行
- -v: 显示版本信息并退出

使用 URL 文件
```bash
export DOWN_FILE=url.txt
./bandwidth_burner -c 64 -t 120
```
`url.txt` 文件格式：
```txt
https://example.com/file1
https://example.com/file2
# 支持注释和空行
```

使用环境变量（优先级低于命令行参数）
```bash
export CONCURRENCY=64
export TIMEOUT=120
```

## 🐳 Docker 使用说明

直接运行
```bash
docker run --rm ghcr.io/beck-8/bandwidth_burner:latest -c 16 -t 60 https://example.com/file1
```
挂载 URL 文件运行
```bash
docker run --rm \
  -e DOWN_FILE=/app/url.txt \
  -v $PWD/url.txt:/app/url.txt \
  ghcr.io/beck-8/bandwidth_burner:latest -c 32 -t 90
```