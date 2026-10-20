package rt

import (
	"testing"
	"time"
)

// A counter per key: activate counts, deactivate records the state, and
// action 1 feeds back event 0.
func counterSpec(activations, deactivated *[]int) *MachineSpec[Unit, int, int, int, int, Unit] {
	return &MachineSpec[Unit, int, int, int, int, Unit]{
		Name: "Counter",
		Init: func(*Ctx) (Unit, error) { return U, nil },
		Activate: func(_ *Ctx, _ Unit, k int) int {
			*activations = append(*activations, k)
			return 0
		},
		On: func(_ *Ctx, _ Unit, s, e int) T2[int, []int] {
			if e == 1 {
				return T2[int, []int]{F0: s + 1, F1: []int{1}}
			}
			return T2[int, []int]{F0: s + 1}
		},
		Act: func(*Ctx, Unit, int, int) Result[[]int, Unit] { return Result_Ok[[]int, Unit]([]int{0}) },
		Deactivate: func(_ *Ctx, _ Unit, _ int, s int) Unit {
			*deactivated = append(*deactivated, s)
			return U
		},
	}
}

func TestMachineTurnsAndIdle(t *testing.T) {
	var acts, deacts []int
	spec := counterSpec(&acts, &deacts)
	clock := NewFakeClock()
	ctx := NewCtx()
	ctx.Clock = clock
	MachineSend(ctx, spec, 7, 1) // turn of 1, then of the fed-back 0
	MachineSend(ctx, spec, 8, 0)
	clock.Advance(time.Minute)
	MachineSend(ctx, spec, 8, 0)
	clock.Advance(4 * time.Minute)
	machineIdle(ctx, spec, 5*time.Minute)
	if len(deacts) != 1 || deacts[0] != 2 {
		t.Fatalf("only key 7 is idle, at state 2: deactivated %v", deacts)
	}
	MachineSend(ctx, spec, 7, 0)
	machineIdle(ctx, spec, 5*time.Minute)
	if len(acts) != 2 || len(deacts) != 1 {
		t.Fatalf("key 7 reloads without activate and is busy again: activations %v, deactivated %v", acts, deacts)
	}
	if got := machineStore(ctx, spec).keys["7"].state; got != 3 {
		t.Fatalf("state of key 7 kept across deactivate: %d", got)
	}
}
