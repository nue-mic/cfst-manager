package engine

// 移植自 XIU2/CloudflareSpeedTest task/download.go (GPL-3.0)。
// 改动：去全局化、进度回调、支持 ctx 取消；上游 -debug 调试输出逐字保留，仅由终端改走 LogFunc 回调。

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/VividCortex/ewma"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36"

// testDownloadSpeed 对延迟测速结果靠前的 IP 逐个做下载测速并按速度排序。
func (r *runner) testDownloadSpeed(ctx context.Context, ipSet pingDelaySet) downloadSpeedSet {
	if r.cfg.Disable { // 禁用下载测速：直接按延迟结果返回
		return downloadSpeedSet(ipSet)
	}
	if len(ipSet) == 0 {
		return downloadSpeedSet{}
	}

	testCount := r.cfg.TestCount
	testNum := testCount
	// IP 不够 或 指定了速度下限 → 可能要把所有 IP 都下载测一遍
	if len(ipSet) < testCount || r.cfg.MinSpeed > 0 {
		testNum = len(ipSet)
	}
	if testNum < testCount {
		testCount = testNum
	}

	// Current=已尝试数, Total=待测队列长度, Available=已达标数。
	// 须每轮上报(而非仅命中时)，否则指定速度下限且大量 IP 不达标时进度会长时间冻结。
	emit := func(attempted, avail int) {
		r.onProgress(Progress{Stage: StageDownload, Current: attempted, Total: testNum, Available: avail})
	}
	emit(0, 0)

	speedSet := make(downloadSpeedSet, 0)
	for i := 0; i < testNum; i++ {
		if ctx.Err() != nil {
			break
		}
		speed, colo := r.downloadHandler(ctx, ipSet[i].ip)
		ipSet[i].downloadSpeed = speed
		if ipSet[i].colo == "" { // httping 已取过 colo 时不覆盖
			ipSet[i].colo = colo
		}
		if speed >= r.cfg.MinSpeed*1024*1024 {
			speedSet = append(speedSet, ipSet[i])
		}
		emit(i+1, len(speedSet))
		if len(speedSet) == testCount {
			break
		}
	}

	if r.cfg.MinSpeed == 0 { // 未指定速度下限：返回全部测速数据
		speedSet = downloadSpeedSet(ipSet)
	} else if r.cfg.Debug && len(speedSet) == 0 {
		// 上游 -debug 行为：指定了速度下限但无 IP 达标时，忽略条件返回全部数据，方便用户调整预期。
		r.debugf("warn", "[调试] 没有满足 下载速度下限 条件的 IP，忽略条件返回所有测速数据（方便下次测速时调整条件）。")
		speedSet = downloadSpeedSet(ipSet)
	}
	sort.Sort(speedSet)
	return speedSet
}

// printDownloadDebugInfo 逐字移植自上游「统一的请求报错调试输出」：按 是否重定向 × 是否有
// HTTP 状态码 组合出 4 种诊断文案。仅在调试模式下产生输出（内部已自行判 Debug）。
func (r *runner) printDownloadDebugInfo(ip *net.IPAddr, err error, statusCode int, url, lastRedirectURL string, resp *http.Response) {
	if !r.cfg.Debug || r.onLog == nil {
		return
	}
	finalURL := url // 默认的最终 URL，这样当 response 为空时也能输出
	if lastRedirectURL != "" {
		finalURL = lastRedirectURL // lastRedirectURL 非空：重定向过，优先输出最后一次要重定向至的目标
	} else if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String() // 否则若 resp 非 nil，取最后一次成功的响应地址
	}
	if url != finalURL { // URL 与最终地址不一致：有重定向，是重定向后的地址引起的错误
		if statusCode > 0 { // 状态码大于 0：HTTP 状态码引起的错误
			r.debugf("err", "[调试] IP: %s, 下载测速终止，HTTP 状态码: %d, 下载测速地址: %s, 出错的重定向后地址: %s", ip.String(), statusCode, url, finalURL)
		} else {
			r.debugf("err", "[调试] IP: %s, 下载测速失败，错误信息: %v, 下载测速地址: %s, 出错的重定向后地址: %s", ip.String(), err, url, finalURL)
		}
	} else { // URL 与最终地址一致：没有重定向
		if statusCode > 0 {
			r.debugf("err", "[调试] IP: %s, 下载测速终止，HTTP 状态码: %d, 下载测速地址: %s", ip.String(), statusCode, url)
		} else {
			r.debugf("err", "[调试] IP: %s, 下载测速失败，错误信息: %v, 下载测速地址: %s", ip.String(), err, url)
		}
	}
}

// getDialContext 返回一个把任意目标地址强制拨到指定 IP:Port 的拨号器。
func (r *runner) getDialContext(ip *net.IPAddr) func(ctx context.Context, network, address string) (net.Conn, error) {
	var fakeAddr string
	if isIPv4(ip.String()) {
		fakeAddr = fmt.Sprintf("%s:%d", ip.String(), r.cfg.TCPPort)
	} else {
		fakeAddr = fmt.Sprintf("[%s]:%d", ip.String(), r.cfg.TCPPort)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fakeAddr)
	}
}

// downloadHandler 对单个 IP 做下载测速，返回平均速度(B/s)与地区码。
func (r *runner) downloadHandler(ctx context.Context, ip *net.IPAddr) (float64, string) {
	var lastRedirectURL string // 记录最后一次重定向目标，供出错时调试输出（移植自上游）
	client := &http.Client{
		Transport: &http.Transport{DialContext: r.getDialContext(ip)},
		Timeout:   r.timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			lastRedirectURL = req.URL.String() // 记录每次重定向的目标
			if len(via) > 10 {                 // 限制最多重定向 10 次
				r.debugf("err", "[调试] IP: %s, 下载测速地址重定向次数过多，终止测速，下载测速地址: %s", ip.String(), req.URL.String())
				return http.ErrUseLastResponse
			}
			if req.Header.Get("Referer") == DefaultURL {
				req.Header.Del("Referer")
			}
			return nil
		},
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.URL, nil)
	if err != nil {
		r.debugf("err", "[调试] IP: %s, 下载测速请求创建失败，错误信息: %v, 下载测速地址: %s", ip.String(), err, r.cfg.URL)
		return 0, ""
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		r.printDownloadDebugInfo(ip, err, 0, r.cfg.URL, lastRedirectURL, resp)
		return 0, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		r.printDownloadDebugInfo(ip, nil, resp.StatusCode, r.cfg.URL, lastRedirectURL, resp)
		return 0, ""
	}

	colo := getHeaderColo(resp.Header)

	timeStart := time.Now()
	timeEnd := timeStart.Add(r.timeout)
	contentLength := resp.ContentLength
	buffer := make([]byte, downloadBufSize)

	var (
		contentRead     int64 = 0
		timeSlice             = r.timeout / 100
		timeCounter           = 1
		lastContentRead int64 = 0
	)
	nextTime := timeStart.Add(timeSlice * time.Duration(timeCounter))
	e := ewma.NewMovingAverage()

	for contentLength != contentRead {
		currentTime := time.Now()
		if currentTime.After(nextTime) {
			timeCounter++
			nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
			e.Add(float64(contentRead - lastContentRead))
			lastContentRead = contentRead
		}
		if currentTime.After(timeEnd) {
			break
		}
		bufferRead, err := resp.Body.Read(buffer)
		if err != nil {
			if err != io.EOF {
				break
			} else if contentLength == -1 { // 文件下完且大小未知
				break
			}
			lastTimeSlice := timeStart.Add(timeSlice * time.Duration(timeCounter-1))
			e.Add(float64(contentRead-lastContentRead) / (float64(currentTime.Sub(lastTimeSlice)) / float64(timeSlice)))
		}
		contentRead += int64(bufferRead)
	}
	return e.Value() / (r.timeout.Seconds() / 120), colo
}
