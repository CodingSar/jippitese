package emit

import "github.com/codingsar/jippitese/compiler/ast"

// walkExpr calls f on e and every expression nested in it, closures
// included, in source order.
func walkExpr(e ast.Expr, f func(ast.Expr)) {
	if e == nil {
		return
	}
	f(e)
	switch x := e.(type) {
	case *ast.StrLit:
		for _, p := range x.Parts {
			walkExpr(p.Expr, f)
		}
	case *ast.FieldExpr:
		walkExpr(x.X, f)
	case *ast.CallExpr:
		walkExpr(x.Fn, f)
		for _, a := range x.Args {
			walkExpr(a, f)
		}
	case *ast.UnaryExpr:
		walkExpr(x.X, f)
	case *ast.BinaryExpr:
		walkExpr(x.L, f)
		walkExpr(x.R, f)
	case *ast.TryExpr:
		walkExpr(x.X, f)
	case *ast.Block:
		walkBlock(x, f)
	case *ast.IfExpr:
		walkExpr(x.Cond, f)
		walkBlock(x.Then, f)
		walkExpr(x.Else, f)
	case *ast.MatchExpr:
		walkExpr(x.X, f)
		for _, a := range x.Arms {
			walkExpr(a.Guard, f)
			walkExpr(a.Body, f)
		}
	case *ast.WhileExpr:
		walkExpr(x.Cond, f)
		walkBlock(x.Body, f)
	case *ast.ForExpr:
		walkExpr(x.Iter, f)
		walkBlock(x.Body, f)
	case *ast.RangeExpr:
		walkExpr(x.Lo, f)
		walkExpr(x.Hi, f)
	case *ast.ReturnExpr:
		walkExpr(x.X, f)
	case *ast.Closure:
		walkExpr(x.Body, f)
	case *ast.RecordLit:
		walkExpr(x.Base, f)
		for _, fi := range x.Fields {
			walkExpr(fi.Value, f)
		}
	case *ast.TupleLit:
		for _, el := range x.Elems {
			walkExpr(el, f)
		}
	case *ast.ListLit:
		for _, el := range x.Elems {
			walkExpr(el, f)
		}
	case *ast.SpawnExpr:
		walkBlock(x.Body, f)
	case *ast.TaskExpr:
		walkExpr(x.X, f)
	case *ast.LlmExpr:
		walkExpr(x.Prompt, f)
		walkExpr(x.Input, f)
	case *ast.DecideExpr:
		walkExpr(x.Input, f)
	case *ast.ApproveExpr:
		walkExpr(x.Review, f)
	case *ast.OldExpr:
		walkExpr(x.X, f)
	case *ast.QuantExpr:
		walkExpr(x.Coll, f)
		walkExpr(x.Body, f)
	case *ast.AnyExpr:
		walkExpr(x.Via, f)
	}
}

func walkBlock(b *ast.Block, f func(ast.Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch st := s.(type) {
		case *ast.LetStmt:
			walkExpr(st.Value, f)
		case *ast.VarStmt:
			walkExpr(st.Value, f)
		case *ast.AssignStmt:
			walkExpr(st.Value, f)
		case *ast.ExprStmt:
			walkExpr(st.X, f)
		}
	}
	walkExpr(b.Tail, f)
}
