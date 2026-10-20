package rt

import (
	"fmt"
	"reflect"
	"testing"
)

type captureLogger struct{ lines []string }

func (c *captureLogger) Logf(format string, args ...any) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func TestLogLevels(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want []string
	}{
		{"", []string{"info: i", "warn: w", "error: e"}},
		{"DEBUG", []string{"debug: d", "info: i", "warn: w", "error: e"}},
		{"error", []string{"error: e"}},
	} {
		t.Setenv("JIP_LOG", tc.env)
		c := &captureLogger{}
		ctx := &Ctx{sh: newShared(c)}
		LogDebug(ctx, "d")
		LogInfo(ctx, "i")
		LogWarn(ctx, "w")
		LogError(ctx, "e")
		if !reflect.DeepEqual(c.lines, tc.want) {
			t.Errorf("JIP_LOG=%q: %q, want %q", tc.env, c.lines, tc.want)
		}
	}
}
