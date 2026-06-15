package engine

// 移植自 XIU2/CloudflareSpeedTest utils/csv.go 的数据结构与排序/过滤逻辑 (GPL-3.0)。
// 改动：类型不再导出（引擎对外只暴露 Result），过滤阈值由参数传入而非全局变量。

import (
	"net"
	"time"
)

// cfData 是单个 IP 的内部测速数据（对应 CFST 的 CloudflareIPData）。
type cfData struct {
	ip            *net.IPAddr
	sended        int
	received      int
	delay         time.Duration
	colo          string
	lossRate      float32
	downloadSpeed float64
}

func (d *cfData) getLossRate() float32 {
	if d.lossRate == 0 && d.sended > 0 {
		d.lossRate = float32(d.sended-d.received) / float32(d.sended)
	}
	return d.lossRate
}

func (d *cfData) toResult() Result {
	colo := d.colo
	if colo == "" {
		colo = "N/A"
	}
	return Result{
		IP:        d.ip.String(),
		Sended:    d.sended,
		Received:  d.received,
		LossRate:  float64(d.getLossRate()),
		DelayMS:   float64(d.delay.Microseconds()) / 1000.0,
		SpeedMBps: d.downloadSpeed / 1024 / 1024,
		Colo:      colo,
	}
}

// pingDelaySet 按 丢包率→延迟 升序排序。
type pingDelaySet []cfData

func (s pingDelaySet) Len() int { return len(s) }
func (s pingDelaySet) Less(i, j int) bool {
	a, b := s[i].getLossRate(), s[j].getLossRate()
	if a != b {
		return a < b
	}
	return s[i].delay < s[j].delay
}
func (s pingDelaySet) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// filterDelay 按平均延迟上下限过滤。仅当阈值非默认值时才过滤（与 CFST 行为一致）。
func (s pingDelaySet) filterDelay(maxDelay, minDelay time.Duration) pingDelaySet {
	const defMax = DefaultMaxDelay * time.Millisecond
	const defMin = DefaultMinDelay * time.Millisecond
	if maxDelay > defMax || minDelay < defMin {
		return s
	}
	if maxDelay == defMax && minDelay == defMin {
		return s
	}
	out := make(pingDelaySet, 0, len(s))
	for _, v := range s {
		if v.delay > maxDelay { // 已按延迟升序，后面都更大，可直接停止
			break
		}
		if v.delay < minDelay {
			continue
		}
		out = append(out, v)
	}
	return out
}

// filterLossRate 按丢包率上限过滤。仅当阈值非默认(1.0)时才过滤。
func (s pingDelaySet) filterLossRate(maxLoss float32) pingDelaySet {
	if maxLoss >= DefaultMaxLossRate {
		return s
	}
	out := make(pingDelaySet, 0, len(s))
	for _, v := range s {
		if v.getLossRate() > maxLoss { // 已按丢包率升序
			break
		}
		out = append(out, v)
	}
	return out
}

// downloadSpeedSet 按下载速度降序排序。
type downloadSpeedSet []cfData

func (s downloadSpeedSet) Len() int           { return len(s) }
func (s downloadSpeedSet) Less(i, j int) bool { return s[i].downloadSpeed > s[j].downloadSpeed }
func (s downloadSpeedSet) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (s downloadSpeedSet) toResults() []Result {
	out := make([]Result, 0, len(s))
	for i := range s {
		out = append(out, s[i].toResult())
	}
	return out
}
