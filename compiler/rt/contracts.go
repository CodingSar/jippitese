package rt

import "fmt"

// Contracts says which contract kinds are checked at run time. The app
// level (`contracts: all | pre | none`) sets it at startup; `jip test`
// forces all three on.
var Contracts = struct{ Pre, Post, Inv bool }{Pre: true, Post: false, Inv: false}

// SetContracts applies an app-level contract setting.
func SetContracts(level string) {
	switch level {
	case "all", "":
		Contracts.Pre, Contracts.Post, Contracts.Inv = true, true, true
	case "pre":
		Contracts.Pre, Contracts.Post, Contracts.Inv = true, false, false
	case "none":
		Contracts.Pre, Contracts.Post, Contracts.Inv = false, false, false
	default:
		panic("rt: unknown contract level " + level)
	}
}

// ContractError is raised when a contract fails.
type ContractError struct {
	Kind string // "pre", "post" or "inv"
	Expr string
	At   string
}

func (e *ContractError) Error() string {
	return fmt.Sprintf("%s violated: %s at %s", e.Kind, e.Expr, e.At)
}

// ContractFail raises a contract failure.
func ContractFail(ctx *Ctx, kind, expr, at string) {
	panic(&ContractError{Kind: kind, Expr: expr, At: at})
}

// Violation converts a contract failure into the prelude record.
func (e *ContractError) Violation() ContractViolation {
	return ContractViolation{Kind: e.Kind, Expr: e.Expr, At: e.At}
}
