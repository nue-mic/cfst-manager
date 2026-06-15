package engine

import (
	"net"
	"testing"
	"time"
)

func TestNormalizeDefaults(t *testing.T) {
	var c Config
	c.Normalize()
	if c.Routines != DefaultRoutines || c.PingTimes != DefaultPingTimes || c.TCPPort != DefaultTCPPort {
		t.Fatalf("默认值未正确填充: %+v", c)
	}
	c2 := Config{Routines: 99999, MaxLossRate: 5}
	c2.Normalize()
	if c2.Routines != MaxRoutines {
		t.Fatalf("线程上限未生效: %d", c2.Routines)
	}
	if c2.MaxLossRate != 1 {
		t.Fatalf("丢包率上限未夹紧: %v", c2.MaxLossRate)
	}
}

func mk(ip string, delay time.Duration, sended, recv int) cfData {
	return cfData{ip: &net.IPAddr{IP: net.ParseIP(ip)}, delay: delay, sended: sended, received: recv}
}

func TestLossRateAndResult(t *testing.T) {
	d := mk("1.1.1.1", 100*time.Millisecond, 4, 3)
	if lr := d.getLossRate(); lr < 0.24 || lr > 0.26 {
		t.Fatalf("丢包率计算错误: %v", lr)
	}
	r := d.toResult()
	if r.IP != "1.1.1.1" || r.DelayMS < 99 || r.DelayMS > 101 {
		t.Fatalf("结果转换错误: %+v", r)
	}
	if r.Colo != "N/A" {
		t.Fatalf("空 colo 应显示 N/A, got %q", r.Colo)
	}
}

func TestFilterDelay(t *testing.T) {
	s := pingDelaySet{
		mk("1.1.1.1", 50*time.Millisecond, 1, 1),
		mk("1.1.1.2", 150*time.Millisecond, 1, 1),
		mk("1.1.1.3", 300*time.Millisecond, 1, 1),
	}
	out := s.filterDelay(200*time.Millisecond, 100*time.Millisecond)
	if len(out) != 1 || out[0].ip.String() != "1.1.1.2" {
		t.Fatalf("延迟过滤错误: %+v", out)
	}
}

func TestFilterLossRate(t *testing.T) {
	s := pingDelaySet{
		mk("1.1.1.1", 50*time.Millisecond, 4, 4), // loss 0
		mk("1.1.1.2", 60*time.Millisecond, 4, 1), // loss 0.75
	}
	out := s.filterLossRate(0.5)
	if len(out) != 1 || out[0].ip.String() != "1.1.1.1" {
		t.Fatalf("丢包过滤错误: %+v", out)
	}
}
