// Package ast is the syntax tree of .jip files.
package ast

import (
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/lexer"
)

type Pos = lexer.Pos

// File is one parsed .jip file.
type File struct {
	Path      string
	Module    []string // module path segments; nil for app.jip
	Uses      []*Use
	Items     []Item
	Scenarios []*Scenario
	App       *AppBlock
}

type Use struct {
	Pos   Pos
	Path  []string
	Alias string
}

// Item is a top-level declaration.
type Item interface{ ItemPos() Pos }

type Visibility int

const (
	Private Visibility = iota
	PubModule
	Pub
)

type TypeParam struct {
	Name   string
	Bounds []string
}

type Field struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

type TypeKind int

const (
	RecordKind TypeKind = iota
	NewtypeKind
	AliasKind
	ExternKind // `extern type Name<Params> = go "pkg".Ident` (stdlib only)
)

type TypeDecl struct {
	Pos    Pos
	Doc    string
	Vis    Visibility
	Opaque bool
	Name   string
	Params []TypeParam
	Kind   TypeKind
	Fields []Field  // RecordKind
	Inner  TypeExpr // NewtypeKind, AliasKind
	Inv    []Expr
	Extern *ExternRef // ExternKind
}

func (d *TypeDecl) ItemPos() Pos { return d.Pos }

type Variant struct {
	Pos        Pos
	Name       string
	Positional []TypeExpr
	Named      []Field
}

type EnumDecl struct {
	Pos      Pos
	Doc      string
	Vis      Visibility
	Name     string
	Params   []TypeParam
	Variants []Variant
}

func (d *EnumDecl) ItemPos() Pos { return d.Pos }

type Effect struct {
	Pos  Pos
	Name string
	Role string
}

// EffectOrder is the canonical order of effects (spec 03 rule 4).
var EffectOrder = map[string]int{"io": 0, "net": 1, "mut": 2, "llm": 3, "decide": 4, "nondet": 5, "spawn": 6, "approve": 7}

// SortEffects orders a set canonically.
func SortEffects(es []Effect) []Effect {
	out := append([]Effect(nil), es...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if EffectOrder[a.Name] != EffectOrder[b.Name] {
			return EffectOrder[a.Name] < EffectOrder[b.Name]
		}
		return a.Role < b.Role
	})
	return out
}

// EffectSetString prints `{io, net, llm<smart>}` in canonical order.
func EffectSetString(es []Effect) string {
	var parts []string
	for _, e := range SortEffects(es) {
		parts = append(parts, e.String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (e Effect) String() string {
	if e.Role != "" {
		return e.Name + "<" + e.Role + ">"
	}
	return e.Name
}

type Param struct {
	Pos  Pos
	Name string
	Type TypeExpr // nil for self or untyped closure params
	Self bool
}

type ExternRef struct {
	Pkg   string
	Ident string
}

type FnDecl struct {
	Pos        Pos
	Doc        string
	Vis        Visibility
	Name       string
	TypeParams []TypeParam
	Params     []Param
	Ret        TypeExpr // nil = ()
	Effects    []Effect
	HasWith    bool
	Pre, Post  []Expr
	Body       *Block // nil for extern or behaviour signature
	Extern     *ExternRef
	Unsafe     bool
	UnsafeWhy  string
}

func (d *FnDecl) ItemPos() Pos { return d.Pos }

type AssocType struct {
	Name string
	Type TypeExpr
}

type ConstDecl struct {
	Pos   Pos
	Doc   string
	Vis   Visibility
	Name  string
	Type  TypeExpr
	Value Expr
}

func (d *ConstDecl) ItemPos() Pos { return d.Pos }

type ImplDecl struct {
	Pos           Pos
	Doc           string
	Behaviour     string     // "" for inherent impl
	BehaviourArgs []TypeExpr // `impl Repository<Order, OrderId> for Orders`
	Target        TypeExpr   // the type (with params for builtin impls)
	Assoc         []AssocType
	Consts        []*ConstDecl
	Methods       []*FnDecl
	Inv           []Expr
}

func (d *ImplDecl) ItemPos() Pos { return d.Pos }

type BehaviourDecl struct {
	Pos     Pos
	Doc     string
	Vis     Visibility
	Name    string
	Params  []TypeParam
	Assoc   []string
	Consts  []*ConstDecl // Type set, Value nil
	Methods []*FnDecl
	Inv     []Expr
}

func (d *BehaviourDecl) ItemPos() Pos { return d.Pos }

// ---- types

type TypeExpr interface{ TypePos() Pos }

type TypePath struct {
	Pos  Pos
	Path []string // e.g. ["Int"], ["http","Error"], ["Self"]
	Args []TypeExpr
}

func (t *TypePath) TypePos() Pos { return t.Pos }

type TupleType struct {
	Pos   Pos
	Elems []TypeExpr // empty = unit
}

func (t *TupleType) TypePos() Pos { return t.Pos }

type FnType struct {
	Pos     Pos
	Params  []TypeExpr
	Ret     TypeExpr
	Effects []Effect
}

func (t *FnType) TypePos() Pos { return t.Pos }

// ---- expressions

type Expr interface{ ExprPos() Pos }

type IntLit struct {
	Pos Pos
	V   int64
	Raw string // source text, digit separators kept; "" when synthesised
}
type FloatLit struct {
	Pos Pos
	V   float64
	Raw string
}
type BoolLit struct {
	Pos Pos
	V   bool
}
type CharLit struct {
	Pos Pos
	V   rune
}
type BytesLit struct {
	Pos Pos
	V   []byte
}
type DurationLit struct {
	Pos Pos
	Ns  int64
	Raw string
}
type StrPart struct {
	Lit  string
	Expr Expr // non-nil for interpolation
}
type StrLit struct {
	Pos   Pos
	Parts []StrPart
}
type Ident struct {
	Pos  Pos
	Name string
}
type FieldExpr struct {
	Pos  Pos
	X    Expr
	Name string // field, method, variant or tuple index ("0")
}
type CallExpr struct {
	Pos      Pos
	Fn       Expr
	TypeArgs []TypeExpr
	Args     []Expr
	Sugar    string // "every" or "cron" when desugared from a schedule literal
}
type UnaryExpr struct {
	Pos Pos
	Op  string
	X   Expr
}
type BinaryExpr struct {
	Pos  Pos
	Op   string
	L, R Expr
}
type TryExpr struct {
	Pos Pos
	X   Expr
}
type Block struct {
	Pos   Pos
	Stmts []Stmt
	Tail  Expr // value of the block, nil if none
}
type IfExpr struct {
	Pos  Pos
	Cond Expr
	Then *Block
	Else Expr // *Block, *IfExpr or nil
}
type Arm struct {
	Pos   Pos
	Pat   Pattern
	Guard Expr
	Body  Expr
}
type MatchExpr struct {
	Pos  Pos
	X    Expr
	Arms []Arm
	End  Pos // position of the closing brace
}
type WhileExpr struct {
	Pos  Pos
	Cond Expr
	Body *Block
}
type ForExpr struct {
	Pos  Pos
	Pat  Pattern
	Iter Expr
	Body *Block
}
type RangeExpr struct {
	Pos    Pos
	Lo, Hi Expr
	Incl   bool
}
type ReturnExpr struct {
	Pos Pos
	X   Expr
}
type BreakExpr struct{ Pos Pos }
type ContinueExpr struct{ Pos Pos }
type Closure struct {
	Pos    Pos
	Params []Param
	Ret    TypeExpr
	Body   Expr
}
type FieldInit struct {
	Name  string
	Value Expr
}
type RecordLit struct {
	Pos    Pos
	Path   []string
	Base   Expr
	Fields []FieldInit
}
type TupleLit struct {
	Pos   Pos
	Elems []Expr
}
type ListLit struct {
	Pos   Pos
	Elems []Expr
}
type SpawnExpr struct {
	Pos  Pos
	Body *Block
}
type TaskExpr struct {
	Pos Pos
	X   Expr
}
type LlmExpr struct {
	Pos    Pos
	Role   string
	Prompt Expr
	Input  Expr
}
type DecideExpr struct {
	Pos   Pos
	Role  string
	Input Expr
}
type ApproveExpr struct {
	Pos    Pos
	Type   TypeExpr
	Review Expr
}
type OldExpr struct {
	Pos Pos
	X   Expr
}
type ToolsExpr struct { // `tools [f, g]`, the `const tools` of an Agent
	Pos Pos
	Fns []*Ident
}
type RetExpr struct{ Pos Pos }
type QuantExpr struct {
	Pos    Pos
	Forall bool
	Var    string
	Coll   Expr
	Body   Expr
}
type AnyExpr struct { // scenario `any T [via g] [where expr]`
	Pos   Pos
	Type  TypeExpr
	Via   Expr
	Where Expr   // inline filter; sees the candidate under Name
	Name  string // the `given` binding, set by the parser
}
type SelfExpr struct{ Pos Pos }

func (e *IntLit) ExprPos() Pos       { return e.Pos }
func (e *FloatLit) ExprPos() Pos     { return e.Pos }
func (e *BoolLit) ExprPos() Pos      { return e.Pos }
func (e *CharLit) ExprPos() Pos      { return e.Pos }
func (e *BytesLit) ExprPos() Pos     { return e.Pos }
func (e *DurationLit) ExprPos() Pos  { return e.Pos }
func (e *StrLit) ExprPos() Pos       { return e.Pos }
func (e *Ident) ExprPos() Pos        { return e.Pos }
func (e *FieldExpr) ExprPos() Pos    { return e.Pos }
func (e *CallExpr) ExprPos() Pos     { return e.Pos }
func (e *UnaryExpr) ExprPos() Pos    { return e.Pos }
func (e *BinaryExpr) ExprPos() Pos   { return e.Pos }
func (e *TryExpr) ExprPos() Pos      { return e.Pos }
func (e *Block) ExprPos() Pos        { return e.Pos }
func (e *IfExpr) ExprPos() Pos       { return e.Pos }
func (e *MatchExpr) ExprPos() Pos    { return e.Pos }
func (e *WhileExpr) ExprPos() Pos    { return e.Pos }
func (e *ForExpr) ExprPos() Pos      { return e.Pos }
func (e *RangeExpr) ExprPos() Pos    { return e.Pos }
func (e *ReturnExpr) ExprPos() Pos   { return e.Pos }
func (e *BreakExpr) ExprPos() Pos    { return e.Pos }
func (e *ContinueExpr) ExprPos() Pos { return e.Pos }
func (e *Closure) ExprPos() Pos      { return e.Pos }
func (e *RecordLit) ExprPos() Pos    { return e.Pos }
func (e *TupleLit) ExprPos() Pos     { return e.Pos }
func (e *ListLit) ExprPos() Pos      { return e.Pos }
func (e *SpawnExpr) ExprPos() Pos    { return e.Pos }
func (e *TaskExpr) ExprPos() Pos     { return e.Pos }
func (e *LlmExpr) ExprPos() Pos      { return e.Pos }
func (e *DecideExpr) ExprPos() Pos   { return e.Pos }
func (e *ApproveExpr) ExprPos() Pos  { return e.Pos }
func (e *OldExpr) ExprPos() Pos      { return e.Pos }
func (e *ToolsExpr) ExprPos() Pos    { return e.Pos }
func (e *RetExpr) ExprPos() Pos      { return e.Pos }
func (e *QuantExpr) ExprPos() Pos    { return e.Pos }
func (e *AnyExpr) ExprPos() Pos      { return e.Pos }
func (e *SelfExpr) ExprPos() Pos     { return e.Pos }

// ---- statements

type Stmt interface{ StmtPos() Pos }

type LetStmt struct {
	Pos   Pos
	Pat   Pattern
	Type  TypeExpr
	Value Expr
}
type VarStmt struct {
	Pos   Pos
	Name  string
	Type  TypeExpr
	Value Expr
}
type AssignStmt struct {
	Pos    Pos
	Target *Ident
	Op     string // "=", "+=", ...
	Value  Expr
}
type ExprStmt struct {
	Pos Pos
	X   Expr
}

func (s *LetStmt) StmtPos() Pos    { return s.Pos }
func (s *VarStmt) StmtPos() Pos    { return s.Pos }
func (s *AssignStmt) StmtPos() Pos { return s.Pos }
func (s *ExprStmt) StmtPos() Pos   { return s.Pos }

// ---- patterns

type Pattern interface{ PatPos() Pos }

type WildPat struct{ Pos Pos }
type BindPat struct {
	Pos  Pos
	Name string
}
type LitPat struct {
	Pos Pos
	Lit Expr
}
type FieldPat struct {
	Name string
	Pat  Pattern
}
type VariantPat struct {
	Pos        Pos
	Path       []string
	Positional []Pattern
	Named      []FieldPat
	Parens     bool
	Braces     bool
}
type TuplePat struct {
	Pos   Pos
	Elems []Pattern
}
type RecordPat struct {
	Pos    Pos
	Path   []string
	Fields []FieldPat
}
type OrPat struct {
	Pos  Pos
	Alts []Pattern
}
type AtPat struct {
	Pos  Pos
	Name string
	Pat  Pattern
}

func (p *WildPat) PatPos() Pos    { return p.Pos }
func (p *BindPat) PatPos() Pos    { return p.Pos }
func (p *LitPat) PatPos() Pos     { return p.Pos }
func (p *VariantPat) PatPos() Pos { return p.Pos }
func (p *TuplePat) PatPos() Pos   { return p.Pos }
func (p *RecordPat) PatPos() Pos  { return p.Pos }
func (p *OrPat) PatPos() Pos      { return p.Pos }
func (p *AtPat) PatPos() Pos      { return p.Pos }

// ---- scenarios

type Tag struct {
	Name string
	Arg  string
}

type GivenKind int

const (
	GivenExpr GivenKind = iota
	GivenAny
	GivenTable
	GivenRealTime
)

type Given struct {
	Pos  Pos
	Kind GivenKind
	Name string   // GivenExpr / GivenAny
	Pat  Pattern  // GivenTable
	Rows Expr     // GivenTable
	Expr Expr     // GivenExpr
	Any  *AnyExpr // GivenAny
}

type When struct {
	Pos  Pos
	Name string // "_" allowed
	Expr Expr
}

type Scenario struct {
	Pos    Pos
	Tags   []Tag
	Name   string
	Givens []Given
	Whens  []When
	Thens  []Expr
}

// ---- app.jip

type AppValue interface{ AppPos() Pos }

type AppBlock struct {
	Pos     Pos
	Head    string // "app", "run X", "env prod", "net", ...
	Entries []*AppEntry
}

type AppEntry struct {
	Pos     Pos
	Key     string   // "name", "gateway_url", "telemetry.sample"
	Default bool     // `default` marker
	Value   AppValue // *AppBlock, *AppLit, *AppCall, *AppPath, *AppType, *AppList
}

type AppLit struct {
	Pos  Pos
	X    Expr
	Unit string // `tokens` in `2_000_000 tokens`; "" otherwise
}
type AppCall struct {
	Pos  Pos
	Name string
	Args []AppValue
	Kw   []*AppEntry
}
type AppPath struct {
	Pos  Pos
	Path []string
}
type AppType struct {
	Pos Pos
	T   TypeExpr
}
type AppList struct {
	Pos   Pos
	Elems []AppValue
}

func (b *AppBlock) AppPos() Pos { return b.Pos }
func (b *AppLit) AppPos() Pos   { return b.Pos }
func (b *AppCall) AppPos() Pos  { return b.Pos }
func (b *AppPath) AppPos() Pos  { return b.Pos }
func (b *AppType) AppPos() Pos  { return b.Pos }
func (b *AppList) AppPos() Pos  { return b.Pos }
