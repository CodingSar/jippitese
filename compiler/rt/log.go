package rt

import (
	"os"
	"strings"
)

// std.log writes leveled lines through the runtime logger. JIP_LOG sets the
// lowest level written (debug, info, warn or error); the default is info.

var logLevels = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

func logAt(ctx *Ctx, level, msg string) Unit {
	min, ok := logLevels[strings.ToLower(os.Getenv("JIP_LOG"))]
	if !ok {
		min = 1
	}
	if logLevels[level] >= min {
		ctx.Logf("%s: %s", level, msg)
	}
	return Unit{}
}

// LogDebug implements `std.log.debug`.
func LogDebug(ctx *Ctx, msg string) Unit { return logAt(ctx, "debug", msg) }

// LogInfo implements `std.log.info`.
func LogInfo(ctx *Ctx, msg string) Unit { return logAt(ctx, "info", msg) }

// LogWarn implements `std.log.warn`.
func LogWarn(ctx *Ctx, msg string) Unit { return logAt(ctx, "warn", msg) }

// LogError implements `std.log.error`.
func LogError(ctx *Ctx, msg string) Unit { return logAt(ctx, "error", msg) }
