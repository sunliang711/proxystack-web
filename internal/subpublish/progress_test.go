package subpublish

import (
	"bytes"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 上传进度按间隔报告已传大小、比例、速度和剩余时间，读完后不再报告。
func TestProgressReaderReportsPeriodically(t *testing.T) {
	lines := []string{}
	logf := Logf(func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) })
	data := bytes.Repeat([]byte("x"), 4<<20)
	started := time.Now().Add(-10 * time.Second)
	reader := &progressReader{reader: bytes.NewReader(data), total: int64(len(data)), started: started, last: started, logf: logf}
	buffer := make([]byte, 1<<20)
	_, err := reader.Read(buffer)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "Uploaded 1.0 MB / 4.0 MB (25%)")
	require.Contains(t, lines[0], "remaining")
	_, err = reader.Read(buffer)
	require.NoError(t, err)
	require.Len(t, lines, 1, "间隔未到不重复报告")
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
	reader.last = time.Now().Add(-progressInterval)
	_, err = reader.Read(buffer)
	require.ErrorIs(t, err, io.EOF)
	require.Len(t, lines, 1, "读完后不再报告进度")

	require.Equal(t, "23.8 MB", formatSize(24961184))
	require.Equal(t, "512 B", formatSize(512))
	require.Equal(t, int64(1000), rate(5000, 5*time.Second))
	require.Equal(t, 2*time.Minute+13*time.Second, roundDuration(2*time.Minute+13*time.Second+400*time.Millisecond))
	var nilLogf Logf
	nilLogf.log("discarded %d", 1)
}
