package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// 版本信息
const (
	Version   = "1.4.0"
	BuildDate = "2025-08-26"
	Author    = "ForwardOptimal - YUNYAN"
)

// 配置结构体
type Config struct {
	BindAddr           string   `json:"bindAddr"`
	Targets            []string `json:"targets"`
	UpdateInterval     int      `json:"updateInterval"`
	FailureInterval    int      `json:"failureInterval"`    // 所有节点失败时的检测间隔
	ProxyProtocol      string   `json:"proxyProtocol"`
}

// 节点健康状态
type NodeHealth struct {
	Address    string
	IsHealthy  bool
	LastCheck  time.Time
	Latency    time.Duration
}

// 当前最优目标和健康状态
var bestTarget string
var nodeHealths map[string]*NodeHealth
var healthMu sync.RWMutex
var mu sync.Mutex

// 检查节点健康状态并测量延迟
func checkNodeHealth(target string) *NodeHealth {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	
	health := &NodeHealth{
		Address:   target,
		LastCheck: time.Now(),
	}
	
	if err != nil {
		health.IsHealthy = false
		health.Latency = 0 // 离线节点延迟为0
		log.Printf("❌ 节点离线: %s (错误: %v)", target, err)
	} else {
		health.IsHealthy = true
		health.Latency = time.Since(start) // 只有在线节点才记录延迟
		conn.Close()
		log.Printf("✅ 节点在线: %s (延迟: %s)", target, formatLatency(health.Latency))
	}
	
	// 更新全局健康状态
	healthMu.Lock()
	if nodeHealths == nil {
		nodeHealths = make(map[string]*NodeHealth)
	}
	nodeHealths[target] = health
	healthMu.Unlock()
	
	return health
}

// 获取所有健康节点
func getHealthyNodes() []*NodeHealth {
	healthMu.RLock()
	defer healthMu.RUnlock()
	
	var healthy []*NodeHealth
	for _, health := range nodeHealths {
		if health.IsHealthy {
			healthy = append(healthy, health)
		}
	}
	return healthy
}

// 检查是否所有节点都失败
func allNodesFailed() bool {
	healthMu.RLock()
	defer healthMu.RUnlock()
	
	if nodeHealths == nil || len(nodeHealths) == 0 {
		return true
	}
	
	for _, health := range nodeHealths {
		if health.IsHealthy {
			return false
		}
	}
	return true
}

// 更新最优目标
func updateBestTarget(targets []string, proxyProtocol string, updateInterval int, failureInterval int) {
	var bestTime time.Duration
	var best string
	var healthyCount int
	
	// 检查所有节点的健康状态
	for _, target := range targets {
		health := checkNodeHealth(target)
		if health.IsHealthy {
			healthyCount++
			if best == "" || health.Latency < bestTime {
				best = target
				bestTime = health.Latency
			}
		}
	}
	
	mu.Lock()
	if healthyCount > 0 {
		bestTarget = best
	} else {
		bestTarget = "" // 所有节点都失败时清空最优目标
	}
	mu.Unlock()
	
	log.Printf("==============================")
	log.Printf("节点健康检查报告 - %s", time.Now().Format("2006-01-02 15:04:05"))
	if bestTarget != "" {
		log.Printf("✓ 最优目标: %s (延迟: %s)", bestTarget, formatLatency(bestTime))
		log.Printf("✓ 健康节点: %d/%d", healthyCount, len(targets))
		log.Printf("✓ 服务状态: 正常运行")
		log.Printf("✓ 下次检查: %d秒后", updateInterval)
	} else {
		log.Printf("✗ 警告: 所有节点都失败了! (%d/%d)", healthyCount, len(targets))
		log.Printf("✗ 服务状态: 已暂停，拒绝新连接")
		log.Printf("✗ 重试间隔: %d秒", failureInterval)
	}
	log.Printf("✓ Proxy Protocol: %s", getProxyProtocolStatus(proxyProtocol))
	
	// 显示详细的节点状态
	log.Printf("--- 节点详细状态 ---")
	healthMu.RLock()
	for _, target := range targets {
		if health, exists := nodeHealths[target]; exists {
			if health.IsHealthy {
				log.Printf("  ✅ 在线 %s (延迟: %s)", target, formatLatency(health.Latency))
			} else {
				log.Printf("  ❌ 离线 %s", target)
			}
		} else {
			log.Printf("  ⚪ %s (未检测)", target)
		}
	}
	healthMu.RUnlock()
	log.Printf("==============================")
}

// 获取 Proxy Protocol 状态字符串
func getProxyProtocolStatus(proxyProtocol string) string {
	if proxyProtocol == "" {
		return "disabled"
	}
	return proxyProtocol
}

// 格式化延迟显示为毫秒
func formatLatency(latency time.Duration) string {
	if latency == 0 {
		return "N/A"
	}
	ms := float64(latency.Nanoseconds()) / 1000000.0
	return fmt.Sprintf("%.2fms", ms)
}

// 启动 TCP 服务
func startTCPServer(bindAddr string, targets []string, updateInterval int, failureInterval int, proxyProtocolName string, proxyProtocol ProxyProtocol) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		log.Fatalf("❌ 无法绑定端口 %s: %v", bindAddr, err)
	}
	defer listener.Close()
	log.Printf("🎉 TCP转发服务器启动成功!")
	log.Printf("🔗 监听地址: %s", bindAddr)
	log.Printf("🎯 目标节点数量: %d", len(targets))

	updateBestTarget(targets, proxyProtocolName, updateInterval, failureInterval)

	go func() {
		for {
			// 根据节点状态决定等待时间
			var interval int
			if allNodesFailed() {
				interval = failureInterval
			} else {
				interval = updateInterval
			}
			
			time.Sleep(time.Duration(interval) * time.Second)
			updateBestTarget(targets, proxyProtocolName, updateInterval, failureInterval)
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
	
	// 如果没有配置failureInterval，默认设置为5秒
	if config.FailureInterval <= 0 {
		config.FailureInterval = 5
	}
	return &config, nil
}

func main() {
	// 定义命令行参数
	var configFile string
	var showHelp bool
	var showVersion bool
	
	flag.StringVar(&configFile, "c", "config.json", "指定配置文件路径")
	flag.BoolVar(&showHelp, "h", false, "显示帮助信息")
	flag.BoolVar(&showHelp, "help", false, "显示帮助信息")
	flag.BoolVar(&showVersion, "v", false, "显示版本信息")
	flag.BoolVar(&showVersion, "version", false, "显示版本信息")
	
	// 自定义usage函数
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ForwardOptimal - 最优延迟TCP转发 v%s\n\n", Version)
		fmt.Fprintf(os.Stderr, "用法:\n")
		fmt.Fprintf(os.Stderr, "  %s [选项]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "选项:\n")
		fmt.Fprintf(os.Stderr, "  -c <文件路径>    指定配置文件路径 (默认: config.json)\n")
		fmt.Fprintf(os.Stderr, "  -h, -help       显示此帮助信息\n")
		fmt.Fprintf(os.Stderr, "  -v, -version    显示版本信息\n\n")
		fmt.Fprintf(os.Stderr, "示例:\n")
		fmt.Fprintf(os.Stderr, "  %s                           # 使用默认配置文件 config.json\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -c /root/a.json          # 使用指定的配置文件\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -c ./configs/prod.json   # 使用相对路径配置文件\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n作者: %s | 构建日期: %s\n", Author, BuildDate)
	}
	
	// 解析命令行参数
	flag.Parse()
	
	// 显示帮助信息
	if showHelp {
		flag.Usage()
		os.Exit(0)
	}
	
	// 显示版本信息
	if showVersion {
		fmt.Printf("ForwardOptimal v%s\n", Version)
		fmt.Printf("构建日期: %s\n", BuildDate)
		fmt.Printf("作者: %s\n", Author)
		os.Exit(0)
	}
	
	// 验证配置文件是否存在
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		log.Fatalf("配置文件不存在: %s", configFile)
	}
	
	fmt.Printf("\n")

	log.Printf("🚀 程序启动中...")
	log.Printf("📁 配置文件: %s", configFile)

	config, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("❌ 加载配置文件失败: %v", err)
	}

	log.Printf("✅ 配置加载成功")
	log.Printf("🌐 监听地址: %s", config.BindAddr)
	log.Printf("🎯 目标节点: %v", config.Targets)
	log.Printf("⏱️  检查间隔: %d秒 (故障时: %d秒)", config.UpdateInterval, config.FailureInterval)
	log.Printf("🔗 Proxy Protocol: %s", getProxyProtocolStatus(config.ProxyProtocol))

	proxyProtocol := getProxyProtocol(config.ProxyProtocol)
	startTCPServer(config.BindAddr, config.Targets, config.UpdateInterval, config.FailureInterval, config.ProxyProtocol, proxyProtocol) // 传递 config.ProxyProtocol
}