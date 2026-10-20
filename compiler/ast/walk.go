package ast

// walkExpr calls f on e and every expression nested in it, closures
// included, in source order.
func WalkExpr(e Expr, f func(Expr)) {
	if e == nil {
		return
	}
	f(e)
	switch x := e.(type) {
	case *StrLit:
		for _, p := range x.Parts {
			WalkExpr(p.Expr, f)
		}
	case *FieldExpr:
		WalkExpr(x.X, f)
	case *CallExpr:
		WalkExpr(x.Fn, f)
		for _, a := range x.Args {
			WalkExpr(a, f)
		}
	case *UnaryExpr:
		WalkExpr(x.X, f)
	case *BinaryExpr:
		WalkExpr(x.L, f)
		WalkExpr(x.R, f)
	case *TryExpr:
		WalkExpr(x.X, f)
	case *Block:
		WalkBlock(x, f)
	case *IfExpr:
		WalkExpr(x.Cond, f)
		WalkBlock(x.Then, f)
		WalkExpr(x.Else, f)
	case *MatchExpr:
		WalkExpr(x.X, f)
		for _, a := range x.Arms {
			WalkExpr(a.Guard, f)
			WalkExpr(a.Body, f)
		}
	case *WhileExpr:
		WalkExpr(x.Cond, f)
		WalkBlock(x.Body, f)
	case *ForExpr:
		WalkExpr(x.Iter, f)
		WalkBlock(x.Body, f)
	case *RangeExpr:
		WalkExpr(x.Lo, f)
		WalkExpr(x.Hi, f)
	case *ReturnExpr:
		WalkExpr(x.X, f)
	case *Closure:
		WalkExpr(x.Body, f)
	case *RecordLit:
		WalkExpr(x.Base, f)
		for _, fi := range x.Fields {
			WalkExpr(fi.Value, f)
		}
	case *TupleLit:
		for _, el := range x.Elems {
			WalkExpr(el, f)
		}
	case *ListLit:
		for _, el := range x.Elems {
			WalkExpr(el, f)
		}
	case *SpawnExpr:
		WalkBlock(x.Body, f)
	case *TaskExpr:
		WalkExpr(x.X, f)
	case *LlmExpr:
		WalkExpr(x.Prompt, f)
		WalkExpr(x.Input, f)
	case *DecideExpr:
		WalkExpr(x.Input, f)
	case *ApproveExpr:
		WalkExpr(x.Review, f)
	case *OldExpr:
		WalkExpr(x.X, f)
	case *QuantExpr:
		WalkExpr(x.Coll, f)
		WalkExpr(x.Body, f)
	case *AnyExpr:
		WalkExpr(x.Via, f)
		WalkExpr(x.Where, f)
	}
}

func WalkBlock(b *Block, f func(Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch st := s.(type) {
		case *LetStmt:
			WalkExpr(st.Value, f)
		case *VarStmt:
			WalkExpr(st.Value, f)
		case *AssignStmt:
			WalkExpr(st.Value, f)
		case *ExprStmt:
			WalkExpr(st.X, f)
		}
	}
	WalkExpr(b.Tail, f)
}

// WalkItemExprs calls f on every expression of an item: bodies, contracts,
// invariants, const values and impl members.
func WalkItemExprs(it Item, f func(Expr)) {
	fn := func(d *FnDecl) {
		for _, e := range d.Pre {
			WalkExpr(e, f)
		}
		for _, e := range d.Post {
			WalkExpr(e, f)
		}
		WalkBlock(d.Body, f)
	}
	switch d := it.(type) {
	case *TypeDecl:
		for _, e := range d.Inv {
			WalkExpr(e, f)
		}
	case *FnDecl:
		fn(d)
	case *ConstDecl:
		WalkExpr(d.Value, f)
	case *ImplDecl:
		for _, c := range d.Consts {
			WalkExpr(c.Value, f)
		}
		for _, m := range d.Methods {
			fn(m)
		}
		for _, e := range d.Inv {
			WalkExpr(e, f)
		}
	case *BehaviourDecl:
		for _, m := range d.Methods {
			fn(m)
		}
	}
}

// WalkScenarioExprs calls f on every expression of a scenario.
func WalkScenarioExprs(s *Scenario, f func(Expr)) {
	for _, g := range s.Givens {
		WalkExpr(g.Expr, f)
		WalkExpr(g.Rows, f)
		if g.Any != nil {
			WalkExpr(g.Any, f)
		}
	}
	for _, w := range s.Whens {
		WalkExpr(w.Expr, f)
	}
	for _, t := range s.Thens {
		WalkExpr(t, f)
	}
}

// WalkType calls f on t and every type nested in it.
func WalkType(t TypeExpr, f func(TypeExpr)) {
	if t == nil {
		return
	}
	f(t)
	switch x := t.(type) {
	case *TypePath:
		for _, a := range x.Args {
			WalkType(a, f)
		}
	case *TupleType:
		for _, e := range x.Elems {
			WalkType(e, f)
		}
	case *FnType:
		for _, p := range x.Params {
			WalkType(p, f)
		}
		WalkType(x.Ret, f)
	}
}

// WalkItemTypes calls f on every type written in an item's signatures and
// bodies (annotations, turbofish arguments, closure and approve types).
func WalkItemTypes(it Item, f func(TypeExpr)) {
	fn := func(d *FnDecl) {
		for _, p := range d.Params {
			WalkType(p.Type, f)
		}
		WalkType(d.Ret, f)
	}
	switch d := it.(type) {
	case *TypeDecl:
		for _, fl := range d.Fields {
			WalkType(fl.Type, f)
		}
		WalkType(d.Inner, f)
	case *EnumDecl:
		for _, v := range d.Variants {
			for _, t := range v.Positional {
				WalkType(t, f)
			}
			for _, fl := range v.Named {
				WalkType(fl.Type, f)
			}
		}
	case *FnDecl:
		fn(d)
	case *ConstDecl:
		WalkType(d.Type, f)
	case *ImplDecl:
		for _, t := range d.BehaviourArgs {
			WalkType(t, f)
		}
		WalkType(d.Target, f)
		for _, a := range d.Assoc {
			WalkType(a.Type, f)
		}
		for _, m := range d.Methods {
			fn(m)
		}
	case *BehaviourDecl:
		for _, m := range d.Methods {
			fn(m)
		}
	}
	WalkItemExprs(it, func(e Expr) {
		switch x := e.(type) {
		case *CallExpr:
			for _, t := range x.TypeArgs {
				WalkType(t, f)
			}
		case *Closure:
			for _, p := range x.Params {
				WalkType(p.Type, f)
			}
			WalkType(x.Ret, f)
		case *ApproveExpr:
			WalkType(x.Type, f)
		case *AnyExpr:
			WalkType(x.Type, f)
		}
	})
}
