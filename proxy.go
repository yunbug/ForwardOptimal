package main

import (
    "encoding/binary"
    "fmt"
    "io"
    "log"
    "net"
    "strings"
    "sync"
    "time"
)

// 连接池管理
const (
    bufferSize = 32 * 1024 // 32KB 缓冲区
)

type TargetPool struct {
    pool sync.Pool
    addr string
}

func NewTargetPool(addr string) *TargetPool {
    return &TargetPool{
        addr: addr,
        pool: sync.Pool{
            New: func() interface{} {
                conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
                if err != nil {
                    log.Printf("连接池初始化失败 %s: %v", addr, err)
                    return nil
                }
                return conn
            },
        },
    }
}

func (p *TargetPool) Get() (net.Conn, error) {
    conn := p.pool.Get()
    if conn == nil {
        return net.DialTimeout("tcp", p.addr, 5*time.Second)
    }
    targetConn := conn.(net.Conn)
    if err := targetConn.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
        targetConn.Close()
        return net.DialTimeout("tcp", p.addr, 5*time.Second)
    }
    return targetConn, nil
}

func (p *TargetPool) Put(conn net.Conn) {
    if conn == nil {
        return
    }
    if err := conn.SetDeadline(time.Now().Add(1 * time.Second)); err == nil {
        p.pool.Put(conn)
    } else {
        conn.Close()
    }
}

var targetPools = make(map[string]*TargetPool)
var poolsMu sync.Mutex

func getOrCreatePool(target string) *TargetPool {
    poolsMu.Lock()
    defer poolsMu.Unlock()
    if pool, ok := targetPools[target]; ok {
        return pool
    }
    pool := NewTargetPool(target)
    targetPools[target] = pool
    return pool
}

// PROXY Protocol 接口
type ProxyProtocol interface {
    Generate(clientConn, targetConn net.Conn) ([]byte, error)
}

// PROXY Protocol v1 实现
type ProxyProtocolV1 struct{}

func (p *ProxyProtocolV1) Generate(clientConn, targetConn net.Conn) ([]byte, error) {
    clientAddr := clientConn.RemoteAddr().(*net.TCPAddr)
    targetAddr := targetConn.RemoteAddr().(*net.TCPAddr)
    header := fmt.Sprintf("PROXY TCP%d %s %s %d %d\r\n",
        4, // 仅支持 IPv4，IPv6 需扩展
        clientAddr.IP.String(),
        targetAddr.IP.String(),
        clientAddr.Port,
        targetAddr.Port)
    return []byte(header), nil
}

// PROXY Protocol v2 实现
type ProxyProtocolV2 struct{}

func (p *ProxyProtocolV2) Generate(clientConn, targetConn net.Conn) ([]byte, error) {
    clientAddr := clientConn.RemoteAddr().(*net.TCPAddr)
    targetAddr := targetConn.RemoteAddr().(*net.TCPAddr)
    isIPv6 := clientAddr.IP.To4() == nil || targetAddr.IP.To4() == nil

    var headerLength int
    if isIPv6 {
        headerLength = 16 + 36
    } else {
        headerLength = 16 + 12
    }

    header := make([]byte, headerLength)
    copy(header[:12], []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A})
    header[12] = 0x21

    if isIPv6 {
        header[13] = 0x21
        binary.BigEndian.PutUint16(header[14:16], uint16(36))
        copy(header[16:32], clientAddr.IP.To16())
        copy(header[32:48], targetAddr.IP.To16())
        binary.BigEndian.PutUint16(header[48:50], uint16(clientAddr.Port))
        binary.BigEndian.PutUint16(header[50:52], uint16(targetAddr.Port))
    } else {
        header[13] = 0x11
        binary.BigEndian.PutUint16(header[14:16], uint16(12))
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
    return header, nil
}

// 获取 PROXY Protocol 实例
func getProxyProtocol(version string) ProxyProtocol {
    switch strings.ToLower(version) {
    case "v1":
        return &ProxyProtocolV1{}
    case "v2":
        return &ProxyProtocolV2{}
    default:
        return nil
    }
}

// 流量转发
func forwardTCP(conn net.Conn, proxyProtocol ProxyProtocol) {
    mu.Lock()
    target := bestTarget
    mu.Unlock()

    pool := getOrCreatePool(target)
    targetConn, err := pool.Get()
    if err != nil {
        log.Printf("无法从连接池获取目标连接 %s: %v", target, err)
        conn.Close()
        return
    }
    defer pool.Put(targetConn)

    targetConn.SetDeadline(time.Now().Add(30 * time.Second))
    conn.SetDeadline(time.Now().Add(30 * time.Second))

    if proxyProtocol != nil {
        header, err := proxyProtocol.Generate(conn, targetConn)
        if err != nil {
            log.Printf("生成 PROXY 协议头失败: %v", err)
            conn.Close()
            return
        }
        if err := targetConn.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
            log.Printf("目标连接不可用: %v", err)
            conn.Close()
            return
        }
        targetConn.SetDeadline(time.Now().Add(30 * time.Second))
        if _, err = targetConn.Write(header); err != nil {
            log.Printf("写入 PROXY 协议头失败: %v", err)
            conn.Close()
            return
        }
    }

    var wg sync.WaitGroup
    buf := make([]byte, bufferSize)
    errChan := make(chan error, 2) // 用于收集转发错误

    wg.Add(1)
    go func() {
        defer wg.Done()
        if _, err := io.CopyBuffer(targetConn, conn, buf); err != nil {
            errChan <- fmt.Errorf("从客户端到目标转发出错: %v", err)
        }
    }()

    wg.Add(1)
    go func() {
        defer wg.Done()
        if _, err := io.CopyBuffer(conn, targetConn, buf); err != nil {
            errChan <- fmt.Errorf("从目标到客户端转发出错: %v", err)
        }
    }()

    go func() {
        wg.Wait()
        close(errChan)
        conn.Close()
        targetConn.Close()
    }()

    // 收集并日志记录错误（仅记录非正常关闭的错误）
    for err := range errChan {
        if !strings.Contains(err.Error(), "i/o timeout") && !strings.Contains(err.Error(), "broken pipe") && !strings.Contains(err.Error(), "closed") {
            log.Printf("%v", err)
        }
    }
}

// 处理客户端连接
func handleConnection(conn net.Conn, proxyProtocol ProxyProtocol) {
    go forwardTCP(conn, proxyProtocol)
}