package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// 配置结构体
type Config struct {
	BindAddr     string   `json:"bindAddr"`
	Targets      []string `json:"targets"`
	UpdateInterval int    `json:"updateInterval"`
}

// 当前最优目标
var bestTarget string
var mu sync.Mutex

// 测量目标的响应时间
func measureLatency(target string) time.Duration {
	start := time.Now()
	conn, err := net.Dial("tcp", target)
	if err != nil {
		//log.Printf("节点异常：无法连接到目标 %s: %v", target, err)
		log.Printf("节点异常：无法连接到目标 %s", target)
		return time.Hour // 无法连接时认为延迟为无限大
		
	}
	defer conn.Close()

	// 计算连接的延迟时间
	duration := time.Since(start)
	return duration
}

// 更新最优目标
func updateBestTarget(targets []string) {
	var bestTime time.Duration
	var best string

	for _, target := range targets {
		latency := measureLatency(target)
		log.Printf("目标 %s 的延迟: %v", target, latency)

		// 第一次或找到更优目标
		if best == "" || latency < bestTime {
			best = target
			bestTime = latency
		}
	}

	mu.Lock()
	bestTarget = best
	mu.Unlock()

	log.Printf("------------------------------")
	log.Printf("最优目标是: ")
	log.Printf("IP： %s", bestTarget)
	log.Printf("------------------------------")

}

// 通过当前最优目标转发流量
func forwardTCP(conn net.Conn) {
	mu.Lock()
	target := bestTarget
	mu.Unlock()

	// 连接到最优目标
	targetConn, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("无法连接到最优目标 %s: %v", target, err)
		conn.Close()
		return
	}
	defer targetConn.Close()

	// 开始转发数据
	go func() {
		_, err := io.Copy(targetConn, conn)
		if err != nil {
			log.Printf("从客户端到目标转发出错: %v", err)
		}
	}()
	_, err = io.Copy(conn, targetConn)
	if err != nil {
		log.Printf("从目标到客户端转发出错: %v", err)
	}
}

// 处理客户端连接
func handleConnection(conn net.Conn) {
	// 只转发到最优目标
	go forwardTCP(conn)
}

// 启动 TCP 服务并定期更新最优目标
func startTCPServer(bindAddr string, targets []string, updateInterval int) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		log.Fatalf("无法绑定端口 %s: %v", bindAddr, err)
		os.Exit(1)
	}
	defer listener.Close()

	log.Printf("TCP 转发服务器正在 %s 上运行", bindAddr)

	// 每隔 `updateInterval` 秒更新一次最优目标
	ticker := time.NewTicker(time.Duration(updateInterval) * time.Second)
	defer ticker.Stop()

	// 初始化最优目标
	updateBestTarget(targets)

	// 启动定时任务定期更新最优目标
	go func() {
		for range ticker.C {
			updateBestTarget(targets)
		}
	}()

	// 处理每个连接
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("连接接受失败: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// 读取配置文件
func loadConfig(configFile string) (*Config, error) {
	file, err := os.Open(configFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var config Config
	err = decoder.Decode(&config)
	if err != nil {
		return nil, err
	}

	// 配置文件必须包含 bindAddr、targets 和 updateInterval 字段
	if config.BindAddr == "" || len(config.Targets) == 0 || config.UpdateInterval <= 0 {
		return nil, fmt.Errorf("配置文件中必须包含 bindAddr、targets 和有效的 updateInterval 字段")
	}

	return &config, nil
}

func main() {
	// 从配置文件加载配置
	configFile := "config.json" // 配置文件路径
	config, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("无法加载配置文件: %v", err)
	}

	// 使用配置中的 bindAddr、targets 和 updateInterval
	bindAddr := config.BindAddr
	targets := config.Targets
	updateInterval := config.UpdateInterval

	// 启动 TCP 服务
	startTCPServer(bindAddr, targets, updateInterval)
}
