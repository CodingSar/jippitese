package rt

import "math"

// ---- std.math -------------------------------------------------------------

func MathSqrt(ctx *Ctx, x float64) float64     { return math.Sqrt(x) }
func MathPow(ctx *Ctx, b, e float64) float64   { return math.Pow(b, e) }
func MathExp(ctx *Ctx, x float64) float64      { return math.Exp(x) }
func MathLn(ctx *Ctx, x float64) float64       { return math.Log(x) }
func MathLog10(ctx *Ctx, x float64) float64    { return math.Log10(x) }
func MathLog2(ctx *Ctx, x float64) float64     { return math.Log2(x) }
func MathSin(ctx *Ctx, x float64) float64      { return math.Sin(x) }
func MathCos(ctx *Ctx, x float64) float64      { return math.Cos(x) }
func MathTan(ctx *Ctx, x float64) float64      { return math.Tan(x) }
func MathAsin(ctx *Ctx, x float64) float64     { return math.Asin(x) }
func MathAcos(ctx *Ctx, x float64) float64     { return math.Acos(x) }
func MathAtan(ctx *Ctx, x float64) float64     { return math.Atan(x) }
func MathAtan2(ctx *Ctx, y, x float64) float64 { return math.Atan2(y, x) }
func MathHypot(ctx *Ctx, x, y float64) float64 { return math.Hypot(x, y) }
func MathPi(ctx *Ctx) float64                  { return math.Pi }
func MathE(ctx *Ctx) float64                   { return math.E }
