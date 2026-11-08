package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/mihomoapi"
)

// byteServer 按 bytes 参数返回指定字节数，每写一块后停顿 pause，用于模拟有限带宽。
func byteServer(t *testing.T, pause time.Duration, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		size, err := strconv.Atoi(r.URL.Query().Get("bytes"))
		if err != nil {
			size = 1 << 20
		}
		w.Header().Set("Content-Length", strconv.Itoa(size))
		chunk := make([]byte, 32<<10)
		for size > 0 {
			n := min(size, len(chunk))
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			size -= n
			if pause > 0 {
				select {
				case <-time.After(pause):
				case <-r.Context().Done():
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestSpeedPlanValidation 验证模式、范围和地址的校验，以及时长模式的流量上限与大小模式的时长上限。
func TestSpeedPlanValidation(t *testing.T) {
	plan, err := newSpeedPlan(SpeedTestOptions{Mode: SpeedModeDuration, Seconds: 10})
	require.NoError(t, err)
	require.Equal(t, DefaultSpeedURL, plan.url)
	require.Equal(t, int64(speedDurationByteCap), plan.limit())
	require.Equal(t, "https://speed.cloudflare.com/__down?bytes=50000000", plan.requestURL(plan.limit()))
	require.Equal(t, "https://speed.cloudflare.com/__down?bytes=1234", plan.requestURL(1234))

	plan, err = newSpeedPlan(SpeedTestOptions{Mode: SpeedModeSize, MB: 50, URL: "http://example.com/file.bin"})
	require.NoError(t, err)
	require.Equal(t, int64(50_000_000), plan.limit())
	require.Equal(t, speedSizeTimeout, plan.timeout)
	require.Equal(t, "http://example.com/file.bin", plan.requestURL(10))

	for _, options := range []SpeedTestOptions{
		{Mode: SpeedModeDuration, Seconds: 4},
		{Mode: SpeedModeDuration, Seconds: 61},
		{Mode: SpeedModeSize, MB: 9},
		{Mode: SpeedModeSize, MB: 1001},
		{Mode: "upload", Seconds: 10},
		{Mode: SpeedModeDuration, Seconds: 10, URL: "ftp://example.com/file"},
	} {
		_, err := newSpeedPlan(options)
		require.Error(t, err, "%+v", options)
	}
}

// TestRunSpeedTestSize 验证大小模式恰好下载目标字节数，按单次请求上限分多次请求，并报告进度与链路查询回调。
func TestRunSpeedTestSize(t *testing.T) {
	var requests atomic.Int32
	server := byteServer(t, 0, &requests)
	plan := speedPlan{url: server.URL + "/?bytes={bytes}", template: true, target: 3 * 1_000_000, timeout: 10 * time.Second, firstByte: 5 * time.Second}
	var looked atomic.Int32
	result := runSpeedTest(context.Background(), server.Client(), plan, func() { looked.Add(1) }, nil)
	require.Empty(t, result.Error)
	require.Equal(t, SpeedStopSize, result.StopReason)
	require.Equal(t, int64(3_000_000), result.Bytes)
	require.Equal(t, int32(1), requests.Load())
	require.Equal(t, int32(1), looked.Load())
	require.Positive(t, result.AvgMbps)
}

// TestRunSpeedTestRepeatsRequests 验证地址不带 {bytes} 时重复请求直到达到目标字节数。
func TestRunSpeedTestRepeatsRequests(t *testing.T) {
	var requests atomic.Int32
	server := byteServer(t, 0, &requests)
	plan := speedPlan{url: server.URL + "/?bytes=400000", target: 1_000_000, timeout: 10 * time.Second, firstByte: 5 * time.Second}
	result := runSpeedTest(context.Background(), server.Client(), plan, nil, nil)
	require.Equal(t, SpeedStopSize, result.StopReason)
	require.Equal(t, int64(1_000_000), result.Bytes)
	require.Equal(t, int32(3), requests.Load())
}

// TestRunSpeedTestDuration 验证时长模式按首字节后的时长停止并推送进度。
func TestRunSpeedTestDuration(t *testing.T) {
	var requests atomic.Int32
	server := byteServer(t, 10*time.Millisecond, &requests)
	plan := speedPlan{url: server.URL + "/?bytes={bytes}", template: true, duration: 1500 * time.Millisecond, byteCap: speedDurationByteCap, timeout: 10 * time.Second, firstByte: 5 * time.Second}
	var reports atomic.Int32
	started := time.Now()
	result := runSpeedTest(context.Background(), server.Client(), plan, nil, func(SpeedTestProgress) { reports.Add(1) })
	require.Less(t, time.Since(started), 4*time.Second)
	require.Empty(t, result.Error)
	require.Equal(t, SpeedStopDuration, result.StopReason)
	require.Equal(t, int64(1500), result.ElapsedMS)
	require.Positive(t, result.Bytes)
	require.GreaterOrEqual(t, reports.Load(), int32(2))
	require.Positive(t, result.PeakMbps)
}

// TestRunSpeedTestErrors 验证非 200 响应、首字节超时和取消的结束原因。
func TestRunSpeedTestErrors(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) }))
	defer failing.Close()
	result := runSpeedTest(context.Background(), failing.Client(), speedPlan{url: failing.URL, target: 1000, timeout: 5 * time.Second, firstByte: 5 * time.Second}, nil, nil)
	require.Equal(t, SpeedStopError, result.StopReason)
	require.Contains(t, result.Error, "403")

	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stalled.Close()
	result = runSpeedTest(context.Background(), stalled.Client(), speedPlan{url: stalled.URL, target: 1000, timeout: 5 * time.Second, firstByte: 300 * time.Millisecond}, nil, nil)
	require.Equal(t, SpeedStopTimeout, result.StopReason)
	require.NotEmpty(t, result.Error)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	result = runSpeedTest(ctx, stalled.Client(), speedPlan{url: stalled.URL, target: 1000, timeout: 5 * time.Second, firstByte: 5 * time.Second}, nil, nil)
	require.Equal(t, SpeedStopCancelled, result.StopReason)
}

// TestSummarize 验证稳定速度排除第一秒，峰值取一秒窗口内的最高速度。
func TestSummarize(t *testing.T) {
	samples := []speedSample{{0, 0}}
	// 第一秒 1 MB，之后每 0.5 秒 1 MB，第 3 秒起的一秒内 3 MB。
	samples = append(samples,
		speedSample{500 * time.Millisecond, 500_000},
		speedSample{time.Second, 1_000_000},
		speedSample{1500 * time.Millisecond, 2_000_000},
		speedSample{2 * time.Second, 3_000_000},
		speedSample{2500 * time.Millisecond, 4_000_000},
		speedSample{3 * time.Second, 5_000_000},
		speedSample{4 * time.Second, 8_000_000},
	)
	avg, stable, peak := summarize(samples)
	require.InDelta(t, 16.0, avg, 0.001)
	require.InDelta(t, 18.666, stable, 0.001)
	require.InDelta(t, 24.0, peak, 0.001)

	avg, stable, peak = summarize([]speedSample{{0, 0}, {500 * time.Millisecond, 1_000_000}})
	require.InDelta(t, 16.0, avg, 0.001)
	require.Equal(t, avg, stable)
	require.Equal(t, avg, peak)
}

// TestMatchConnection 验证优先按本端端口找到测速连接，端口不匹配时只在目标域名唯一时匹配。
func TestMatchConnection(t *testing.T) {
	connection := func(port, host string) mihomoapi.Connection {
		var c mihomoapi.Connection
		c.Metadata.SourcePort, c.Metadata.Host = port, host
		return c
	}
	connections := []mihomoapi.Connection{connection("1000", "speed.cloudflare.com"), connection("2000", "speed.cloudflare.com"), connection("3000", "example.com")}
	require.Equal(t, "2000", matchConnection(connections, []string{"2000"}, "speed.cloudflare.com").Metadata.SourcePort)
	require.Nil(t, matchConnection(connections, []string{"9"}, "speed.cloudflare.com"))
	require.Equal(t, "3000", matchConnection(connections, nil, "example.com").Metadata.SourcePort)
	require.Nil(t, matchConnection(connections, nil, "other.com"))
}
