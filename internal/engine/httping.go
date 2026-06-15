package engine

// 移植自 XIU2/CloudflareSpeedTest task/httping.go (GPL-3.0)。
// 改动：去全局化（参数取自 runner.cfg / runner.colomap）、支持 ctx、移除 log.Fatal；
// 上游 -debug 调试输出逐字保留，仅由终端改走 LogFunc 回调。

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	regexpColoIATACode    = regexp.MustCompile(`[A-Z]{3}`)  // IATA 机场三字码
	regexpColoCountryCode = regexp.MustCompile(`[A-Z]{2}`)  // 国家地区码
	regexpColoGcore       = regexp.MustCompile(`^[a-z]{2}`) // Gcore 城市码(小写)
)

// buildColoMap 把 -cfcolo 参数(逗号分隔)解析为大写地区集合; 空串返回 nil(不过滤)。
func buildColoMap(raw string) map[string]struct{} {
	if raw == "" {
		return nil
	}
	m := make(map[string]struct{})
	for _, c := range strings.Split(strings.ToUpper(raw), ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			m[c] = struct{}{}
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// httping 用 HTTP HEAD 请求测延迟，并尝试解析数据中心地区码。
func (r *runner) httping(ctx context.Context, ip *net.IPAddr) (int, time.Duration, string) {
	hc := &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			DialContext: r.getDialContext(ip),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 阻止重定向
		},
	}
	defer hc.CloseIdleConnections()

	// 先访问一次拿 HTTP 状态码与地区码
	var colo string
	{
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, r.cfg.URL, nil)
		if err != nil {
			r.debugf("err", "[调试] IP: %s, 延迟测速请求创建失败，错误信息: %v, 测速地址: %s", ip.String(), err, r.cfg.URL)
			return 0, 0, ""
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := hc.Do(req)
		if err != nil {
			r.debugf("err", "[调试] IP: %s, 延迟测速失败，错误信息: %v, 测速地址: %s", ip.String(), err, r.cfg.URL)
			return 0, 0, ""
		}
		defer resp.Body.Close()

		// 未指定或非法(越界)状态码时，默认认可 200/301/302。
		// 注意：判定须用 || —— 合法状态码在 [100,599]，越界即 code<100 或 code>599。
		// 原 CFST 此处误用 &&（恒为假），导致设置越界自定义码时回退失效，此处修正。
		code := r.cfg.HttpingStatusCode
		if code == 0 || code < 100 || code > 599 {
			if resp.StatusCode != 200 && resp.StatusCode != 301 && resp.StatusCode != 302 {
				r.debugf("err", "[调试] IP: %s, 延迟测速终止，HTTP 状态码: %d, 测速地址: %s", ip.String(), resp.StatusCode, r.cfg.URL)
				return 0, 0, ""
			}
		} else if resp.StatusCode != code {
			r.debugf("err", "[调试] IP: %s, 延迟测速终止，HTTP 状态码: %d, 指定的 HTTP 状态码 %d, 测速地址: %s", ip.String(), resp.StatusCode, code, r.cfg.URL)
			return 0, 0, ""
		}

		io.Copy(io.Discard, resp.Body)
		colo = getHeaderColo(resp.Header)

		// 指定了地区则做匹配
		if r.colomap != nil {
			colo = r.filterColo(colo)
			if colo == "" {
				// 与上游一致：此处 colo 已被 filterColo 覆盖为空，故输出的地区码恒为空字符串。
				r.debugf("err", "[调试] IP: %s, 地区码不匹配: %s", ip.String(), colo)
				return 0, 0, ""
			}
		}
	}

	// 循环测速计算延迟
	success := 0
	var delay time.Duration
	for i := 0; i < r.cfg.PingTimes; i++ {
		if ctx.Err() != nil {
			break
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, r.cfg.URL, nil)
		if err != nil {
			return 0, 0, ""
		}
		req.Header.Set("User-Agent", userAgent)
		if i == r.cfg.PingTimes-1 {
			req.Header.Set("Connection", "close")
		}
		start := time.Now()
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		success++
		io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		delay += time.Since(start)
	}
	return success, delay, colo
}

// filterColo 判断地区码是否在指定集合内; 不在则返回空串。
func (r *runner) filterColo(colo string) string {
	if colo == "" {
		return ""
	}
	if r.colomap == nil {
		return colo
	}
	if _, ok := r.colomap[colo]; ok {
		return colo
	}
	return ""
}

// getHeaderColo 从响应头识别数据中心地区码（兼容 Cloudflare/CDN77/Bunny/CloudFront/Fastly/Gcore）。
func getHeaderColo(header http.Header) string {
	if header.Get("server") != "" {
		// Cloudflare: cf-ray: 7bd32409eda7b020-SJC
		if header.Get("server") == "cloudflare" {
			if colo := header.Get("cf-ray"); colo != "" {
				return regexpColoIATACode.FindString(colo)
			}
		}
		// CDN77-Turbo: x-77-pop: frankfurtDE
		if header.Get("server") == "CDN77-Turbo" {
			if colo := header.Get("x-77-pop"); colo != "" {
				return regexpColoCountryCode.FindString(colo)
			}
		}
		// BunnyCDN: server: BunnyCDN-TW1-1121
		if colo := header.Get("server"); strings.Contains(colo, "BunnyCDN-") {
			return regexpColoCountryCode.FindString(strings.TrimPrefix(colo, "BunnyCDN-"))
		}
	}
	// AWS CloudFront: x-amz-cf-pop: SIN52-P1
	if colo := header.Get("x-amz-cf-pop"); colo != "" {
		return regexpColoIATACode.FindString(colo)
	}
	// Fastly: x-served-by: ...,cache-hhr-khhr2060043-HHR（取最后一个）
	if colo := header.Get("x-served-by"); colo != "" {
		if matches := regexpColoIATACode.FindAllString(colo, -1); len(matches) > 0 {
			return matches[len(matches)-1]
		}
	}
	// Gcore: x-id-fe: fr5-hw-edge-gc17（城市码，小写转大写）
	if colo := header.Get("x-id-fe"); colo != "" {
		if colo = regexpColoGcore.FindString(colo); colo != "" {
			return strings.ToUpper(colo)
		}
	}
	return ""
}
