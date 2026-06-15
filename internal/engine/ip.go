package engine

// 移植自 XIU2/CloudflareSpeedTest task/ip.go (GPL-3.0)。
// 改动：去全局化（IP 来源由 Config 传入）、log.Fatal → error 返回、随机数用包内 rng。

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
)

func isIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}

func randIPEndWith(num byte) byte {
	if num == 0 { // 对于 /32 这种单独的 IP
		return byte(0)
	}
	return byte(rand.Intn(int(num)))
}

type ipRanges struct {
	ips     []*net.IPAddr
	mask    string
	firstIP net.IP
	ipNet   *net.IPNet
	testAll bool
}

func newIPRanges(testAll bool) *ipRanges {
	return &ipRanges{
		ips:     make([]*net.IPAddr, 0),
		testAll: testAll,
	}
}

// 如果是单独 IP 则加上子网掩码，反之则获取子网掩码(r.mask)。
func (r *ipRanges) fixIP(ip string) string {
	if i := strings.IndexByte(ip, '/'); i < 0 {
		if isIPv4(ip) {
			r.mask = "/32"
		} else {
			r.mask = "/128"
		}
		ip += r.mask
	} else {
		r.mask = ip[i:]
	}
	return ip
}

// 解析 IP 段，获得 IP、IP 范围、子网掩码。出错返回 error（原项目为 log.Fatalln）。
func (r *ipRanges) parseCIDR(ip string) error {
	var err error
	if r.firstIP, r.ipNet, err = net.ParseCIDR(r.fixIP(ip)); err != nil {
		return fmt.Errorf("解析 IP 段失败 [%s]: %w", ip, err)
	}
	return nil
}

func (r *ipRanges) appendIPv4(d byte) {
	r.appendIP(net.IPv4(r.firstIP[12], r.firstIP[13], r.firstIP[14], d))
}

func (r *ipRanges) appendIP(ip net.IP) {
	r.ips = append(r.ips, &net.IPAddr{IP: ip})
}

// 返回第四段 IP 的最小值及可用数目。
func (r *ipRanges) getIPRange() (minIP, hosts byte) {
	minIP = r.firstIP[15] & r.ipNet.Mask[3] // IP 第四段最小值

	m := net.IPv4Mask(255, 255, 255, 255)
	for i, v := range r.ipNet.Mask {
		m[i] ^= v
	}
	total, _ := strconv.ParseInt(m.String(), 16, 32) // 总可用 IP 数
	if total > 255 {                                 // 矫正 第四段 可用 IP 数
		hosts = 255
		return
	}
	hosts = byte(total)
	return
}

func (r *ipRanges) chooseIPv4() {
	if r.mask == "/32" { // 单个 IP 无需随机
		r.appendIP(r.firstIP)
		return
	}
	minIP, hosts := r.getIPRange()
	for r.ipNet.Contains(r.firstIP) {
		if r.testAll { // 测速全部 IP
			for i := 0; i <= int(hosts); i++ {
				r.appendIPv4(byte(i) + minIP)
			}
		} else { // 随机最后一段
			r.appendIPv4(minIP + randIPEndWith(hosts))
		}
		r.firstIP[14]++
		if r.firstIP[14] == 0 {
			r.firstIP[13]++
			if r.firstIP[13] == 0 {
				r.firstIP[12]++
			}
		}
	}
}

func (r *ipRanges) chooseIPv6() {
	if r.mask == "/128" { // 单个 IP 无需随机
		r.appendIP(r.firstIP)
		return
	}
	var tempIP uint8
	for r.ipNet.Contains(r.firstIP) {
		r.firstIP[15] = randIPEndWith(255)
		r.firstIP[14] = randIPEndWith(255)

		targetIP := make([]byte, len(r.firstIP))
		copy(targetIP, r.firstIP)
		r.appendIP(targetIP)

		for i := 13; i >= 0; i-- {
			tempIP = r.firstIP[i]
			r.firstIP[i] += randIPEndWith(255)
			if r.firstIP[i] >= tempIP {
				break
			}
		}
	}
}

// loadIPRanges 根据 Config 生成全部待测 IP。IPText 优先于 IPFile。
func loadIPRanges(cfg Config) ([]*net.IPAddr, error) {
	ranges := newIPRanges(cfg.TestAll)

	addOne := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		if err := ranges.parseCIDR(line); err != nil {
			return err
		}
		if isIPv4(line) {
			ranges.chooseIPv4()
		} else {
			ranges.chooseIPv6()
		}
		return nil
	}

	if cfg.IPText != "" { // 从参数获取 IP 段
		for _, ip := range strings.Split(cfg.IPText, ",") {
			if err := addOne(ip); err != nil {
				return nil, err
			}
		}
		return ranges.ips, nil
	}

	// 从文件获取 IP 段
	file := cfg.IPFile
	if file == "" {
		file = DefaultIPFile
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("打开 IP 文件失败 [%s]: %w", file, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := addOne(scanner.Text()); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 IP 文件失败 [%s]: %w", file, err)
	}
	return ranges.ips, nil
}
