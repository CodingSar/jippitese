package checker

import (
	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/diag"
)

// Input is what the loader hands the checker: the parsed app.jip and every
// user module (app modules, dependency modules, scenario modules).
type Input struct {
	App     *app.App
	AppFile *ast.File
	Modules []*ModuleSrc
}

// ModuleSrc is one parsed module.
type ModuleSrc struct {
	Path        string // "orders.billing"
	Dir         string // directory on disk ("" for stdlib)
	Files       []*ast.File
	IsScenarios bool
	IsDep       bool // lives under vendor/<alias>/
}

// Module is a checked module.
type Module struct {
	Path        string
	Dir         string
	Files       []*ast.File
	IsScenarios bool
	IsStd       bool
	IsDep       bool
	Parent      *Module            // module under test, for scenario modules
	Deps        map[string]*Module // alias → module, from `use`
	UseOrder    []string           // aliases in `use` order
	UsePos      map[string]ast.Pos
	UsedAliases map[string]bool
	Types       map[string]*TypeInfo
	Fns         map[string]*FnInfo
	Consts      map[string]*ConstInfo
	Behaviours  map[string]*BehaviourInfo
	Impls       []*ImplInfo
	Scenarios   []*ScenarioInfo
	Order       []string // item names in declaration order (types, fns, consts)
	Externs     []*FnInfo
}

// TypeInfo describes a named type.
type TypeInfo struct {
	Name     string
	Mod      string
	Module   *Module
	Kind     TypeKind
	Params   []*TParam
	Fields   []*FieldInfo   // Record
	Inner    Type           // Newtype payload, Alias target
	Variants []*VariantInfo // Enum
	Vis      ast.Visibility
	Opaque   bool
	Doc      string
	Inv      []ast.Expr
	Decl     ast.Item
	GoPkg    string // Extern
	GoIdent  string
	Pos      ast.Pos
	Impls    []*ImplInfo // inherent impls on this type, in declaration order
	BImpls   []*ImplInfo // behaviour impls targeting this type
}

// FieldInfo is one record field.
type FieldInfo struct {
	Name string
	Type Type
	Pos  ast.Pos
}

// VariantInfo is one enum variant.
type VariantInfo struct {
	Name       string
	Index      int
	Positional []Type
	Named      []*FieldInfo
	Enum       *TypeInfo
	Pos        ast.Pos
}

// FnInfo describes a function or method. Type.Params include `self` first
// when HasSelf.
type FnInfo struct {
	Name       string
	Mod        string
	Module     *Module
	Decl       *ast.FnDecl
	TypeParams []*TParam // impl params first, then the fn's own
	OwnParams  []*TParam
	Type       *TFn
	HasSelf    bool
	Impl       *ImplInfo
	Pos        ast.Pos
	Vis        ast.Visibility
	Doc        string
	Extern     *ast.ExternRef
	Unsafe     bool
	Used       bool
	Calls      []*FnInfo
	HasApprove bool
	Builtin    string // non-empty for checker-synthesised functions (`enqueue`, `fake`)
}

// ConstInfo is a module or impl const.
type ConstInfo struct {
	Name   string
	Module *Module
	Decl   *ast.ConstDecl
	Type   Type
	Impl   *ImplInfo
	Vis    ast.Visibility
	Doc    string
	Pos    ast.Pos
}

// ImplInfo is an `impl T { … }` or `impl B for T { … }` block.
type ImplInfo struct {
	Decl          *ast.ImplDecl
	Module        *Module
	Behaviour     *BehaviourInfo
	BehaviourArgs []Type
	Target        Type
	TargetInfo    *TypeInfo
	TypeParams    []*TParam
	Assoc         map[string]Type
	Consts        map[string]*ConstInfo
	Methods       map[string]*FnInfo
	MethodOrder   []string
	Defaults      []string // behaviour methods taken from the default body
	Inv           []ast.Expr
	Name          string    // target type name, used as the instance name for behaviour impls
	Tools         *TypeInfo // Agent: the generated Tools enum
}

// BehaviourInfo is a `behaviour` declaration.
type BehaviourInfo struct {
	Name        string
	Mod         string
	Module      *Module
	Decl        *ast.BehaviourDecl
	Params      []*TParam
	Self        *TParam
	Assoc       map[string]*TParam
	AssocOrder  []string
	Consts      map[string]Type
	Methods     map[string]*FnInfo
	MethodOrder []string
	Vis         ast.Visibility
}

// ScenarioInfo is one checked scenario.
type ScenarioInfo struct {
	Decl   *ast.Scenario
	Module *Module
	Index  int
}

// Local is a local binding (parameter, let, var, pattern binding).
type Local struct {
	Name     string
	Type     Type
	Var      bool
	Go       string // unique Go name within the function
	Pos      ast.Pos
	Fn       *TFn // non-nil when the local holds a closure with a known effect set
	Effects  []ast.Effect
	Explicit bool
	Captured bool
}

// RefKind says what an identifier resolved to.
type RefKind int

const (
	RefLocal RefKind = iota
	RefFn
	RefConst
	RefType
	RefModule
	RefVariant
	RefTopic
	RefBuiltin
	RefImpl
)

// Ref is the resolution of an *ast.Ident.
type Ref struct {
	Kind    RefKind
	Local   *Local
	Fn      *FnInfo
	Const   *ConstInfo
	Type    *TypeInfo
	Variant *VariantInfo
	Impl    *ImplInfo
	Module  *Module
	Topic   *app.Topic
	Builtin string
}

// CallKind says how a call lowers.
type CallKind int

const (
	CallFn        CallKind = iota // free function, static method, or module function
	CallMethod                    // method with a receiver
	CallInstance                  // behaviour instance by name: Orders.get(k)
	CallVariant                   // enum constructor
	CallNewtype                   // newtype construction
	CallBuiltin                   // run_workflow, tick, send_signal, enqueue, machine_send, publish
	CallClosure                   // call through a local of function type
	CallRecordInv                 // never emitted; reserved
)

// CallInfo is the resolution of an *ast.CallExpr.
type CallInfo struct {
	Kind         CallKind
	Fn           *FnInfo
	Variant      *VariantInfo
	Newtype      *TypeInfo
	TypeArgs     []Type // the function's own type params, in order
	ImplTypeArgs []Type
	Receiver     ast.Expr
	Impl         *ImplInfo
	Builtin      string
	Topic        *app.Topic
	Local        *Local
	Effects      []ast.Effect // effects this call performs (after closure args)
	Step         bool         // journaled step inside Workflow.run
	StepKey      string
}

// FieldKind says what an *ast.FieldExpr denotes.
type FieldKind int

const (
	FieldRecord FieldKind = iota
	FieldTupleIndex
	FieldNewtype0
	FieldVariant  // Enum.Variant without payload
	FieldStaticFn // Type.fn or alias.fn, used as a value
	FieldModuleItem
	FieldTopic
	FieldImpl
)

// FieldRef is the resolution of an *ast.FieldExpr.
type FieldRef struct {
	Kind    FieldKind
	Field   *FieldInfo
	Index   int
	Variant *VariantInfo
	Fn      *FnInfo
	Const   *ConstInfo
	Topic   *app.Topic
	Impl    *ImplInfo
	Type    *TypeInfo
}

// TryInfo is the resolution of `?`.
type TryInfo struct {
	Option bool
	Lift   *VariantInfo // wrapping variant when the error type is lifted
	From   Type
	To     Type
}

// PatInfo is the resolution of a pattern node.
type PatInfo struct {
	Type    Type
	Variant *VariantInfo
	Newtype *TypeInfo
	Record  *TypeInfo
}

// ClosureInfo is the checked type of a closure.
type ClosureInfo struct {
	Type     *TFn
	Effects  []ast.Effect
	Captures []*Local
}

// Program is the checked whole.
type Program struct {
	App       *app.App
	AppName   string
	Modules   []*Module // dependency order, stdlib first
	ByPath    map[string]*Module
	Prelude   *Module
	Testing   *Module
	AppCfg    *Module
	Config    *TypeInfo
	Secrets   *TypeInfo
	Topics    map[string]*app.Topic
	TopicType map[string]Type

	Types         map[ast.Expr]Type
	Refs          map[*ast.Ident]*Ref
	Calls         map[*ast.CallExpr]*CallInfo
	Fields        map[*ast.FieldExpr]*FieldRef
	Tries         map[*ast.TryExpr]*TryInfo
	Pats          map[ast.Pattern]*PatInfo
	RecordLits    map[*ast.RecordLit]*TypeInfo
	Closures      map[*ast.Closure]*ClosureInfo
	PatBinds      map[*ast.BindPat]*Local
	AtBinds       map[*ast.AtPat]*Local
	VarDefs       map[*ast.VarStmt]*Local
	Params        map[*ast.Param]*Local
	QuantLocals   map[*ast.QuantExpr]*Local
	ForLocals     map[*ast.ForExpr]*Local
	GivenLocals   map[*ast.Given]*Local
	WhenLocals    map[*ast.When]*Local
	LlmOut        map[*ast.LlmExpr]Type
	DecideOut     map[*ast.DecideExpr]Type
	ApproveOut    map[*ast.ApproveExpr]Type
	AnyTypes      map[*ast.AnyExpr]Type
	AnyLocals     map[*ast.AnyExpr]*Local      // the candidate a `where` filter sees
	FnEffects     map[*ast.FnDecl][]ast.Effect // effects actually used in the body
	UnsafeExterns []*FnInfo
	// ApproveChannel is the `ask` of the module `effects.approve.channel`
	// names; nil for `std.approve.http`.
	ApproveChannel *FnInfo
}

func newProgram() *Program {
	return &Program{
		ByPath:      map[string]*Module{},
		Topics:      map[string]*app.Topic{},
		TopicType:   map[string]Type{},
		Types:       map[ast.Expr]Type{},
		Refs:        map[*ast.Ident]*Ref{},
		Calls:       map[*ast.CallExpr]*CallInfo{},
		Fields:      map[*ast.FieldExpr]*FieldRef{},
		Tries:       map[*ast.TryExpr]*TryInfo{},
		Pats:        map[ast.Pattern]*PatInfo{},
		RecordLits:  map[*ast.RecordLit]*TypeInfo{},
		Closures:    map[*ast.Closure]*ClosureInfo{},
		PatBinds:    map[*ast.BindPat]*Local{},
		AtBinds:     map[*ast.AtPat]*Local{},
		VarDefs:     map[*ast.VarStmt]*Local{},
		Params:      map[*ast.Param]*Local{},
		QuantLocals: map[*ast.QuantExpr]*Local{},
		ForLocals:   map[*ast.ForExpr]*Local{},
		GivenLocals: map[*ast.Given]*Local{},
		WhenLocals:  map[*ast.When]*Local{},
		LlmOut:      map[*ast.LlmExpr]Type{},
		DecideOut:   map[*ast.DecideExpr]Type{},
		ApproveOut:  map[*ast.ApproveExpr]Type{},
		AnyTypes:    map[*ast.AnyExpr]Type{},
		AnyLocals:   map[*ast.AnyExpr]*Local{},
		FnEffects:   map[*ast.FnDecl][]ast.Effect{},
	}
}

// TypeOf returns the zonked type recorded for an expression.
func (p *Program) TypeOf(e ast.Expr) Type {
	if t, ok := p.Types[e]; ok {
		return Zonk(t)
	}
	return nil
}

// Checker holds the state of one Check run.
type Checker struct {
	bag           *diag.Bag
	prog          *Program
	prelude       *Module
	testing       *Module
	nextVar       int
	u             unifier
	fn            *fnCtx
	mod           *Module
	workflowRuns  []*FnInfo
	approvePos    map[*FnInfo]ast.Pos
	resolving     map[*TypeInfo]bool
	pendingSchema []schemaCheck
	roleReported  map[string]bool
	agentEnums    map[*ast.ImplDecl]*TypeInfo // the generated Tools enum of each Agent impl
	agentTools    *TypeInfo                   // the Tools enum of the Agent impl being declared or checked
	toolsImpl     *ImplInfo                   // the Agent impl whose `const tools` is being checked
}

// fnCtx is the per-function checking state.
type fnCtx struct {
	info          *FnInfo
	ret           Type // nil inside scenarios
	declared      []ast.Effect
	hasWith       bool
	budget        []ast.Effect // behaviour method budget; nil otherwise
	used          map[string]ast.Pos
	inSpawn       int
	inPost        bool
	inContract    bool
	contractWhat  string
	module        *Module
	scopes        []map[string]*Local
	sinks         []*effSet
	goNames       map[string]int
	closureRets   []Type
	loops         int
	isWorkflowRun bool
	locals        []*Local
	localDepth    map[*Local]int
	spawnDepth    []int   // scope depth at which each enclosing spawn began
	diverge       []*TVar // result variables of diverging expressions
	recorded      []ast.Expr
	stepSeq       int
	// budgetReported holds the effects already reported as outside the
	// behaviour budget, so a declared-and-used effect is reported once.
	budgetReported map[string]bool
}

type effSet struct {
	effs     []ast.Effect
	closure  *ast.Closure
	captures map[*Local]bool
	depth    int // scope depth at which the closure began
}
