<h1 align="center">🚀 BandwidthBurner</h1>

<p align="center">
	<a href="https://github.com/beck-8/bandwidth_burner/releases"><img src="https://img.shields.io/github/v/release/beck-8/bandwidth_burner?style=flat-square&include_prereleases&label=version" /></a>
	<a href="https://github.com/beck-8/bandwidth_burner/releases"><img src="https://img.shields.io/github/downloads/beck-8/bandwidth_burner/total.svg?style=flat-square" /></a>
  <a href="https://hub.docker.com/r/beck8/bandwidth_burner/tags"><img src="https://img.shields.io/docker/pulls/beck8/bandwidth_burner" /></a>
	<a href="https://github.com/beck-8/bandwidth_burner/issues"><img src="https://img.shields.io/github/issues-raw/beck-8/bandwidth_burner.svg?style=flat-square&label=issues" /></a>
	<a href="https://github.com/beck-8/bandwidth_burner/graphs/contributors"><img src="https://img.shields.io/github/contributors/beck-8/bandwidth_burner?style=flat-square" /></a>
	<a href="https://github.com/beck-8/bandwidth_burner/blob/master/LICENSE"><img src="https://img.shields.io/github/license/beck-8/bandwidth_burner?style=flat-square" /></a>
</p>

---

带宽消耗下行流量

一个用于带宽压力测试的工具，支持并发下载、流量统计、自动退出等功能。

## 🚀 功能特性

- ✅ 支持自定义HTTP请求头
- ✅ 支持自定义User-Agent或随机UA
- ✅ 支持Keep-Alive连接复用
- ✅ 支持自定义域名解析
- ✅ 实时流量监控与统计
- ✅ 版本信息查看
- ✅ 丰富的环境变量支持


### 参数参考表

| 参数 | 简写 | 默认值 | 描述 | 示例 |
|------|------|--------|------|------|
| concurrency | -c | 32 | 并发下载数量 | `-c 64` |
| timeout | -t | 0 | 运行时间限制(秒) | `-t 300` |
| keep-alives | -k | true | 启用HTTP Keep-Alive | `-k` |
| user-agent | -ua | 随机 | 自定义User-Agent | `-ua "Custom Bot"` |
| header | N/A | 无 | 自定义请求头 | `-h "Auth: Bearer token"` |
| resolve | N/A | 无 | 自定义域名解析 | `-resolve "example.com:80:1.2.3.4"` |
| version | -v | - | 显示版本信息 | `-v` |

### 环境变量支持

| 环境变量 | 对应参数 | 描述 | 示例 |
|----------|----------|------|------|
| CONCURRENCY | -c | 并发数 | `export CONCURRENCY=64` |
| TIMEOUT | -t | 超时时间 | `export TIMEOUT=300` |
| KeepAlives | -k | Keep-Alive开关 | `export KeepAlives=1` |
| UserAgent | -ua | User-Agent字符串 | `export UserAgent="Bot"` |
| DOWN_FILE | - | URL文件路径 | `export DOWN_FILE=urls.txt` |

## 使用示例设计

### 基础使用场景
```bash
# 简单压力测试
./bandwidth_burner https://example.com/large-file.zip

# 指定并发数和时间限制
./bandwidth_burner -c 16 -t 300 https://example.com/file1 https://example.com/file2

# 查看版本信息
./bandwidth_burner -v
```

### 高级功能使用
```bash
# 使用自定义User-Agent和请求头
./bandwidth_burner -ua "LoadTest/1.0" -h "Authorization: Bearer token123" \
  -h "X-Custom-Header: value" https://api.example.com/download

# 启用Keep-Alive并自定义域名解析
./bandwidth_burner -k -resolve "example.com:443:1.2.3.4" \
  https://example.com/large-file.zip

# 使用URL文件进行批量测试
./bandwidth_burner -c 32 -t 600 -k
```

### 环境变量使用
```bash
# 设置环境变量
export CONCURRENCY=64
export TIMEOUT=300
export UserAgent="TestBot/1.0"
export DOWN_FILE=test-urls.txt

# 直接运行（使用环境变量配置）
./bandwidth_burner
```

## 特性功能说明

### 流量监控机制
- 每10秒输出当前流量统计
- 支持总流量(GiB)和平均速度(MB/s)显示  
- 程序退出时显示最终统计数据

### 自定义域名解析
- 支持手动指定域名到IP的映射关系
- 格式：`domain:port:ip`
- 支持IPv4和IPv6地址
- 可同时指定多个映射关系

### 请求头定制
- 支持添加任意自定义HTTP请求头
- 可多次使用`-h`参数添加多个请求头
- 格式：`Key: Value`

### User-Agent配置
- 支持自定义User-Agent字符串
- 不指定时使用随机UA（通过utils/ua.go提供）
- 可通过环境变量UserAgent设置默认值

### Keep-Alive连接管理
- 支持启用/禁用HTTP Keep-Alive
- 启用后可复用TCP连接，提高效率

## URL文件格式说明

当使用DOWN_FILE环境变量指定URL文件时，文件格式如下：

```txt
# 这是注释行，以#开头的行会被忽略
https://cdn1.example.com/file1.zip
https://cdn2.example.com/file2.iso
https://mirror.example.com/package.tar.gz

# 支持空行和注释
https://download.example.com/dataset.bin
```

## Docker使用增强

### 带环境变量的运行
```bash
docker run --rm \
  -e CONCURRENCY=64 \
  -e TIMEOUT=300 \
  -e UserAgent="DockerTest/1.0" \
  ghcr.io/beck-8/bandwidth_burner:latest \
  -k https://example.com/file.zip
```

### 自定义域名解析
```bash
docker run --rm \
  ghcr.io/beck-8/bandwidth_burner:latest \
  -resolve "test.example.com:443:192.168.1.100" \
  https://test.example.com/file.zip
```

## 注意事项与限制

### 性能考虑
- 高并发可能对目标服务器造成压力，请合理设置并发数
- 建议根据网络带宽和系统性能调整并发参数
- 长时间运行时注意监控系统资源使用情况

### 网络配置
- 程序会跳过TLS证书验证（InsecureSkipVerify）
- 支持系统代理设置（ProxyFromEnvironment）
- 连接超时和响应超时已优化为合理默认值

### 兼容性
- 支持HTTP和HTTPS协议
- 兼容IPv4和IPv6网络环境
- 支持各种文件类型的下载测试

## 版本信息显示

使用`-v`参数查看程序版本：
```bash
./bandwidth_burner -v
# 输出示例：
# Version: v1.0.0
# Commit: abc123def
```

## 信号处理与优雅退出

程序支持优雅退出机制：
- `Ctrl+C` (SIGINT) 或 `SIGTERM` 信号触发优雅退出
- 退出时显示最终流量统计信息
- 确保所有协程正确清理资源

## 实时监控输出示例

程序运行过程中的监控信息：
```
程序启动，版本: v1.0.0-abc123def
已配置自定义域名解析: map[example.com:80:1.2.3.4]
当前总共消耗流量: 1.234 GiB，平均速度: 45.67 MB/s
收到终止信号 interrupt，总共消耗流量: 5.678 GiB，平均速度: 38.92 MB/s
```