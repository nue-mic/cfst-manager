package engine

// 移植自 XIU2/CloudflareSpeedTest task/tcping.go (GPL-3.0)。
// 改动：Ping 状态内联进 runner 调用、用进度回调替代进度条、用 DialContext 支持 ctx 取消。

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// runPing 对全部 IP 做延迟测速，返回按 丢包率→延迟 排序的结果集。
func (r *runner) runPing(ctx context.Context, ips []*net.IPAddr) pingDelaySet {
	set := make(pingDelaySet, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	control := make(chan struct{}, r.cfg.Routines)

	total := len(ips)
	var processed int64
	step := total / 200
	if step < 1 {
		step = 1
	}

	emit := func() {
		mu.Lock()
		avail := len(set)
		mu.Unlock()
		r.onProgress(Progress{
			Stage:     StageLatency,
			Current:   int(atomic.LoadInt64(&processed)),
			Total:     total,
			Available: avail,
		})
	}
	emit() // 起始 0/total

	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		control <- struct{}{}
		go func(ip *net.IPAddr) {
			defer wg.Done()
			defer func() { <-control }()

			recv, totalDelay, colo := r.checkConnection(ctx, ip)
			if recv != 0 {
				mu.Lock()
				set = append(set, cfData{
					ip:       ip,
					sended:   r.cfg.PingTimes,
					received: recv,
					delay:    totalDelay / time.Duration(recv),
					colo:     colo,
				})
				mu.Unlock()
			}
			n := atomic.AddInt64(&processed, 1)
			if int(n)%step == 0 || int(n) == total {
				emit()
			}
		}(ip)
	}
	wg.Wait()
	emit() // 收尾
	sort.Sort(set)
	return set
}

// checkConnection 测一个 IP：HTTPing 模式走 httping，否则做 PingTimes 次 TCPing。
func (r *runner) checkConnection(ctx context.Context, ip *net.IPAddr) (recv int, totalDelay time.Duration, colo string) {
	if r.cfg.Httping {
		return r.httping(ctx, ip)
	}
	for i := 0; i < r.cfg.PingTimes; i++ {
		if ctx.Err() != nil {
			return
		}
		if ok, delay := r.tcping(ctx, ip); ok {
			recv++
			totalDelay += delay
		}
	}
	return
}

// tcping 对单个 IP:Port 建立一次 TCP 连接，返回是否成功及耗时。
func (r *runner) tcping(ctx context.Context, ip *net.IPAddr) (bool, time.Duration) {
	var addr string
	if isIPv4(ip.String()) {
		addr = fmt.Sprintf("%s:%d", ip.String(), r.cfg.TCPPort)
	} else {
		addr = fmt.Sprintf("[%s]:%d", ip.String(), r.cfg.TCPPort)
	}
	start := time.Now()
	dialer := &net.Dialer{Timeout: tcpConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, 0
	}
	defer conn.Close()
	return true, time.Since(start)
}
