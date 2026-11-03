// Package ingest 实现两种指标上报通道：
// TCP 行协议（"指标名 数值" 每行一条）和 UDP 数据报，
// 用来演示网络编程、并发连接处理与协议解析。
package ingest

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Handler 接收解析后的一个采样点。
type Handler func(metric string, value float64, at time.Time)

// TCP 是指标采集的 TCP 服务。
type TCP struct {
	addr    string
	log     *slog.Logger
	handler Handler
	ln      net.Listener
	wg      sync.WaitGroup
}

// NewTCP 创建 TCP 采集服务。
func NewTCP(addr string, log *slog.Logger, handler Handler) *TCP {
	return &TCP{addr: addr, log: log, handler: handler}
}

// Start 开始监听，立即返回。
func (t *TCP) Start() error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return err
	}
	t.ln = ln
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.wg.Add(1)
			go func() {
				defer t.wg.Done()
				t.handle(conn)
			}()
		}
	}()
	return nil
}

func (t *TCP) handle(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		metric, value, err := parse(line)
		if err != nil {
			t.log.Warn("bad metric line", "line", line, "error", err)
			continue
		}
		t.handler(metric, value, time.Now())
	}
}

// Close 关闭监听并等待连接处理结束。
func (t *TCP) Close() {
	if t.ln != nil {
		_ = t.ln.Close()
	}
	t.wg.Wait()
}

// UDP 是指标上报的 UDP 服务。
type UDP struct {
	addr    string
	log     *slog.Logger
	handler Handler
	conn    *net.UDPConn
	wg      sync.WaitGroup
}

// NewUDP 创建 UDP 上报服务。
func NewUDP(addr string, log *slog.Logger, handler Handler) *UDP {
	return &UDP{addr: addr, log: log, handler: handler}
}

// Start 开始监听。
func (u *UDP) Start() error {
	addr, err := net.ResolveUDPAddr("udp", u.addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	u.conn = conn
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		buf := make([]byte, 64*1024)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				metric, value, err := parse(line)
				if err != nil {
					continue
				}
				u.handler(metric, value, time.Now())
			}
		}
	}()
	return nil
}

// Close 关闭 UDP 监听。
func (u *UDP) Close() {
	if u.conn != nil {
		_ = u.conn.Close()
	}
	u.wg.Wait()
}

// Serve 阻塞运行（真实服务里由 main 负责生命周期，这里保留给测试）。
func Serve(ctx context.Context, ln net.Listener, handler func(net.Conn)) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go handler(conn)
	}
}

func parse(line string) (string, float64, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return "", 0, errors.New("格式应为: <指标名> <数值>")
	}
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return "", 0, err
	}
	return fields[0], v, nil
}
