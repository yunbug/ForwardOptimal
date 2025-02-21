package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// 配置结构体
type Config struct {
	BindAddr      string   `json:"bindAddr"`
	Targets       []string `json:"targets"`
	UpdateInterval int      `json:"updateInterval"`
	ProxyProtocol string   `json:"proxyProtocol"`
}

// 当前最优目标
var bestTarget string
var mu sync.Mutex

// 测量目标的响应时间
func measureLatency(target string) time.Duration {
	start := time.Now()
	conn, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("节点异常：无法连接到目标 %s", target)
		return time.Hour
	}
	defer conn.Close()
	return time.Since(start)
}

// 更新最优目标
func updateBestTarget(targets []string, proxyProtocol string) {
	var bestTime time.Duration
	var best string
	for _, target := range targets {
		latency := measureLatency(target)
		log.Printf("目标 %s 的延迟: %v", target, latency)
		if best == "" || latency < bestTime {
			best = target
			bestTime = latency
		}
	}
	mu.Lock()
	bestTarget = best
	mu.Unlock()
	log.Printf("------------------------------")
	log.Printf("最优目标是: IP： %s", bestTarget)
	log.Printf("Proxy Protocol 状态: %s", getProxyProtocolStatus(proxyProtocol)) // 输出 Proxy Protocol 状态
	log.Printf("------------------------------")
}

// 获取 Proxy Protocol 状态字符串
func getProxyProtocolStatus(proxyProtocol string) string {
	if proxyProtocol == "" {
		return "disabled"
	}
	return proxyProtocol
}

// 启动 TCP 服务
func startTCPServer(bindAddr string, targets []string, updateInterval int, proxyProtocolName string, proxyProtocol ProxyProtocol) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		log.Fatalf("无法绑定端口 %s: %v", bindAddr, err)
	}
	defer listener.Close()
	log.Printf("TCP 转发服务器正在 %s 上运行", bindAddr)

	ticker := time.NewTicker(time.Duration(updateInterval) * time.Second)
	defer ticker.Stop()

	updateBestTarget(targets, proxyProtocolName) // 传递 proxyProtocolName

	go func() {
		for range ticker.C {
			updateBestTarget(targets, proxyProtocolName) // 传递 proxyProtocolName
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("连接接受失败: %v", err)
			continue
		}
		go handleConnection(conn, proxyProtocol)
	}
}

// 读取配置文件
func loadConfig(configFile string) (*Config, error) {
	file, err := os.Open(configFile)
	if err != nil {
		return nil, fmt.Errorf("无法打开配置文件 %s: %v", configFile, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var config Config
	err = decoder.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("无法解析配置文件 %s: %v", configFile, err)
	}

	if config.BindAddr == "" || len(config.Targets) == 0 || config.UpdateInterval <= 0 {
		return nil, fmt.Errorf("配置文件中必须包含 bindAddr、targets 和有效的 updateInterval 字段")
	}
	return &config, nil
}

func main() {
	defaultConfigFile := "config.json"
	var configFile string
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	} else {
		configFile = defaultConfigFile
	}

	config, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	proxyProtocol := getProxyProtocol(config.ProxyProtocol)
	startTCPServer(config.BindAddr, config.Targets, config.UpdateInterval, config.ProxyProtocol, proxyProtocol) // 传递 config.ProxyProtocol
}