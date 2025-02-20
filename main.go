package main

import (
    "encoding/binary"
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
    BindAddr           string   `json:"bindAddr"`
    Targets            []string `json:"targets"`
    UpdateInterval     int      `json:"updateInterval"`
    EnableProxyProtocol bool     `json:"enableProxyProtocol"` // 是否启用 PROXY 协议
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

// GenerateProxyHeaderV2 生成 PROXY Protocol v2 头部
func GenerateProxyHeaderV2(clientConn, targetConn net.Conn) []byte {
    // 获取客户端和目标的地址信息
    clientAddr := clientConn.RemoteAddr().(*net.TCPAddr)
    targetAddr := targetConn.RemoteAddr().(*net.TCPAddr)

    // 判断地址族
    isIPv6 := clientAddr.IP.To4() == nil || targetAddr.IP.To4() == nil

    // 根据地址族计算头部长度
    var headerLength int
    if isIPv6 {
        headerLength = 16 + 36 // 固定头部 (16 字节) + IPv6 地址 (36 字节)
    } else {
        headerLength = 16 + 12 // 固定头部 (16 字节) + IPv4 地址 (12 字节)
    }

    // 构造 PROXY 协议头
    header := make([]byte, headerLength)
    copy(header[:12], []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}) // 固定头部
    header[12] = 0x21 // 版本和命令字段：v2, PROXY

    if isIPv6 {
        header[13] = 0x21 // 地址族和协议字段：AF_INET6, TCP
        binary.BigEndian.PutUint16(header[14:16], uint16(36)) // IPv6 地址长度 (36 字节)

        // 写入源地址和端口
        copy(header[16:32], clientAddr.IP.To16()) // 源 IPv6 地址
        copy(header[32:48], targetAddr.IP.To16()) // 目标 IPv6 地址
        binary.BigEndian.PutUint16(header[48:50], uint16(clientAddr.Port))
        binary.BigEndian.PutUint16(header[50:52], uint16(targetAddr.Port))
    } else {
        header[13] = 0x11 // 地址族和协议字段：AF_INET, TCP
        binary.BigEndian.PutUint16(header[14:16], uint16(12)) // IPv4 地址长度 (12 字节)

        // 写入源地址和端口
        binary.BigEndian.PutUint32(header[16:20], uint32(clientAddr.IP.To4()[0])<<24|
            uint32(clientAddr.IP.To4()[1])<<16|
            uint32(clientAddr.IP.To4()[2])<<8|
            uint32(clientAddr.IP.To4()[3]))
        binary.BigEndian.PutUint32(header[20:24], uint32(targetAddr.IP.To4()[0])<<24|
            uint32(targetAddr.IP.To4()[1])<<16|
            uint32(targetAddr.IP.To4()[2])<<8|
            uint32(targetAddr.IP.To4()[3]))
        binary.BigEndian.PutUint16(header[24:26], uint16(clientAddr.Port))
        binary.BigEndian.PutUint16(header[26:28], uint16(targetAddr.Port))
    }

    return header
}

// 通过当前最优目标转发流量
func forwardTCP(conn net.Conn, enableProxyProtocol bool) {
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

    // 如果启用了 PROXY 协议，则插入 PROXY 协议头
    if enableProxyProtocol {
        proxyHeader := GenerateProxyHeaderV2(conn, targetConn)
        _, err = targetConn.Write(proxyHeader)
        if err != nil {
            log.Printf("写入 PROXY 协议头失败: %v", err)
            conn.Close()
            targetConn.Close()
            return
        }
    }

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
func handleConnection(conn net.Conn, enableProxyProtocol bool) {
    // 只转发到最优目标
    go forwardTCP(conn, enableProxyProtocol)
}

// 启动 TCP 服务并定期更新最优目标
func startTCPServer(bindAddr string, targets []string, updateInterval int, enableProxyProtocol bool) {
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
        go handleConnection(conn, enableProxyProtocol)
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

    // 配置文件必须包含 bindAddr、targets 和 updateInterval 字段
    if config.BindAddr == "" || len(config.Targets) == 0 || config.UpdateInterval <= 0 {
        return nil, fmt.Errorf("配置文件中必须包含 bindAddr、targets 和有效的 updateInterval 字段")
    }
    return &config, nil
}

func main() {
    // 默认配置文件路径
    defaultConfigFile := "config.json"

    // 获取命令行参数
    var configFile string
    if len(os.Args) > 1 {
        configFile = os.Args[1]
    } else {
        configFile = defaultConfigFile
    }

    // 加载配置文件
    config, err := loadConfig(configFile)
    if err != nil {
        log.Fatalf("加载配置文件失败: %v", err)
    }

    // 使用配置中的 bindAddr、targets 和 updateInterval
    bindAddr := config.BindAddr
    targets := config.Targets
    updateInterval := config.UpdateInterval
    enableProxyProtocol := config.EnableProxyProtocol

    // 启动 TCP 服务
    startTCPServer(bindAddr, targets, updateInterval, enableProxyProtocol)
}