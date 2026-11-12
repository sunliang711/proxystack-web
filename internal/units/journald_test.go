package units

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadJournaldRateLimit(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	commented := write("main.conf", "[Journal]\n#RateLimitIntervalSec=30s\n#RateLimitBurst=10000\n")
	disabled := write("10-off.conf", "[Journal]\nRateLimitIntervalSec=0s\n")
	raised := write("20-burst.conf", "[Journal]\nRateLimitBurst = 500000\n")
	lowered := write("30-burst.conf", "[Journal]\nRateLimitIntervalSec=1min\nRateLimitBurst=2000\n")

	for _, tc := range []struct {
		name    string
		files   []string
		limited bool
	}{
		{name: "defaults", files: []string{commented, filepath.Join(dir, "missing.conf")}, limited: true},
		{name: "interval zero disables", files: []string{commented, disabled}, limited: false},
		{name: "large burst", files: []string{commented, raised}, limited: false},
		{name: "later file wins", files: []string{commented, disabled, raised, lowered}, limited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readJournaldRateLimit(tc.files).limited(); got != tc.limited {
				t.Fatalf("limited = %v, want %v", got, tc.limited)
			}
		})
	}
	// unit 自身设置的突发上限覆盖全局值，间隔非零时覆盖全局的「不限速」。
	unit := readJournaldRateLimit([]string{commented, disabled})
	unit.intervalZero, unit.burst = false, 1000000
	if unit.limited() {
		t.Fatal("unit burst 1000000 should not be limited")
	}
}
