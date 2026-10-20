# Grammar

The complete surface grammar of jippitese v0, consolidated from the
`## Grammar` block of each section. Where a section block and this file
differ, this file is normative and the section block is the subset that
section needed; the differences are listed under Reconciliations at the end.

Notation: `Name ::= …` defines a nonterminal; `"x"` is a literal token; `A B`
is sequence; `A | B` is choice; `A?`, `A*`, `A+` are optional, zero-or-more
and one-or-more; `( … )` groups; `/* … */` is a comment; `…` in a token
range means the inclusive range. Lowercase names (`letter`, `digit`,
`decimal`, `newline`) are lexical fragments; `NEWLINE` is a statement
terminator emitted by the lexer only after the tokens of 01 rule 16, so a
line that ends in `{`, `,`, `(` or an operator continues. Whitespace and
`Comment` are skipped between tokens. Precedence and associativity are
encoded in the `Expr` chain and in 04 rule 2.

## Lexical (01)

```
Ident       ::= letter (letter | digit | "_")* /* not a keyword, not "_" */
/* Contextual words, identifiers elsewhere (01 rule 5): `opaque`, `pre`,
   `post`, `inv`, `default`, `every`, `cron`, `real_time`. */
TypeName    ::= Ident /* PascalCase by convention */
letter      ::= "A" … "Z" | "a" … "z" | "_"
digit       ::= "0" … "9"

Literal     ::= IntLit | FloatLit | StrLit | RawStrLit | BytesLit
                | CharLit | DurationLit | "true" | "false"
IntLit      ::= decimal | "0x" hexdigit (hexdigit | "_")*
decimal     ::= digit (digit | "_")*
FloatLit    ::= decimal "." decimal (("e" | "E") ("-" | "+")? decimal)?
StrLit      ::= '"' (strchar | escape | interp | "{{" | "}}")* '"'
interp      ::= "{" Expr "}" /* no '"' inside */
RawStrLit   ::= 'r"' (any except '"')* '"'
BytesLit    ::= 'b"' (asciichar | byteescape)* '"'
CharLit     ::= "'" (strchar | escape) "'"
DurationLit ::= decimal ("ns" | "us" | "ms" | "s" | "m" | "h")

Comment     ::= "//" (any except newline)*
DocLine     ::= "///" (any except newline)* NEWLINE
NEWLINE     ::= newline /* only after the tokens of 01 rule 16 */
```

## Files and items (01, 02, 09, 12)

```
File        ::= ModuleDecl UseDecl* (Item* | ScenarioBody)
ModuleDecl  ::= "module" ModulePath NEWLINE
UseDecl     ::= "use" UsePath ("as" Ident)? NEWLINE
ModulePath  ::= Ident ("." Ident)*
UsePath     ::= "std" "." Ident | Ident /* dependency alias, 09 rule 4 */ | ModulePath

Item        ::= DocLine* Visibility? "opaque"? (TypeDecl | EnumDecl | FnDecl | ConstDecl
                | BehaviourDecl | ImplDecl | ExternDecl)
                /* "opaque" only with pub type / pub enum; DocLine required on pub items */
Visibility  ::= "pub" ("(" "module" ")")?
ConstDecl   ::= "const" Ident (":" Type)? "=" Expr NEWLINE
```

A file whose module path ends in `.scenarios` has a `ScenarioBody` and no
`Item` (12 rule 1); every other file has `Item*`.

## Types (02)

```
TypeDecl    ::= "type" TypeName TypeParams? TypeBody NEWLINE InvClause*
TypeBody    ::= "{" FieldList "}" /* record */
                | "(" Type ")" /* newtype */
                | "=" Type /* alias; no InvClause */
FieldList   ::= (Field ("," Field)* ","?)?
Field       ::= Ident ":" Type
EnumDecl    ::= "enum" TypeName TypeParams? "{" Variant ("," Variant)* ","? "}" NEWLINE
Variant     ::= TypeName ("(" Type ("," Type)* ")" | "{" FieldList "}")?

TypeParams  ::= "<" TypeParam ("," TypeParam)* ">"
TypeParam   ::= TypeName (":" Bound ("+" Bound)*)? | EffectVar
Bound       ::= TypePath
EffectVar   ::= Ident /* lowercase; an effect-set variable (03 rule 9) */

Type        ::= TypePath TypeArgs? /* Int, Money, List<T>, customers.Id */
                | "(" (Type ("," Type)*)? ")" /* tuple or unit */
                | FnType
                | TypeName "." TypeName /* associated type: Self.Input, T.Output */
FnType      ::= "fn" "(" (Type ("," Type)*)? ")" "->" Type ("with" EffectExpr)?
TypePath    ::= (Ident ".")? TypeName
TypeArgs    ::= "<" Type ("," Type)* ">"
```

## Functions and effects (03)

```
FnDecl      ::= "fn" Ident TypeParams? "(" Params? ")" ("->" Type)? ("with" EffectExpr)?
                ContractLine* Block
                /* omitted "->" means "-> ()"; omitted "with" means pure */
Params      ::= Param ("," Param)* ","?
Param       ::= "self" | Ident ":" Type

EffectExpr  ::= EffectSet | EffectVar | EffectExpr "+" EffectExpr
EffectSet   ::= "{" (Effect ("," Effect)*)? "}"
Effect      ::= "io" | "net" | "mut" | "nondet" | "spawn" | "approve"
                | "llm" "<" RoleLit ">" | "decide" "<" RoleLit ">"
RoleLit     ::= Ident /* a role from app.jip models {} */
```

## Contracts (05)

```
ContractLine ::= PreClause | PostClause | InvClause
PreClause    ::= "pre" Expr NEWLINE
PostClause   ::= "post" Expr NEWLINE
InvClause    ::= "inv" Expr NEWLINE
ContractExpr ::= "ret" | "old" "(" Expr ")" | Quantifier
Quantifier   ::= ("forall" | "exists") Ident "in" Expr ":" Expr
```

Placement: `pre` and `post` follow a `FnDecl` or `FnSig` signature; `inv`
follows a `TypeDecl`, a `BehaviourDecl` item list or an `ImplDecl` item list.
`ContractExpr` forms are valid only inside a `ContractLine`: `ret` only in
`post`, `old(e)` only in `post` with `e` pure over parameter names (05 rule 4).
An `impl` method may add `post` but never `pre` (06 rule 4).

## Behaviours and impls (06, 09)

```
BehaviourDecl ::= "behaviour" TypeName TypeParams? "{" BehaviourItem* "}"
BehaviourItem ::= AssocTypeSig | ConstSig | FnSig | FnDecl | InvClause
AssocTypeSig  ::= "type" TypeName NEWLINE
ConstSig      ::= "const" Ident ":" Type NEWLINE
FnSig         ::= "fn" Ident TypeParams? "(" Params? ")" ("->" Type)? ("with" EffectExpr)?
                  NEWLINE ContractLine*

ImplDecl      ::= "impl" (TypeName TypeArgs? "for")? Type "{" ImplItem* "}"
ImplItem      ::= AssocType | ImplConst | InvClause | FnDecl
AssocType     ::= "type" TypeName "=" Type NEWLINE
ImplConst     ::= "const" Ident "=" ConstExpr NEWLINE
ConstExpr     ::= Expr | ScheduleLit
ScheduleLit   ::= "every" DurationLit | "cron" StrLit
```

An inherent `impl T { … }` (no `for`, 03 rule 4, R3) holds `FnDecl` and
`InvClause` items only; `AssocType` and `ImplConst` appear in behaviour impls
only. `RoleLit` constants (`const role = fast`) parse as `Expr` whose value
is a bare `Ident`.

## Expressions (04, 05, 07, 08)

```
Block       ::= "{" (Stmt NEWLINE)* Expr? "}"
Stmt        ::= "let" Pattern (":" Type)? "=" Expr
                | "var" Ident (":" Type)? "=" Expr
                | Ident AssignOp Expr
                | "while" CondExpr Block
                | "for" Pattern "in" CondExpr Block
                | "break"
                | "continue"
                | "return" Expr?
                | Expr
AssignOp    ::= "=" | "+=" | "-=" | "*=" | "/="

Expr        ::= OrExpr ((".." | "..=") OrExpr)?
CondExpr    ::= Expr /* no unparenthesised RecordExpr at top level: `if x { … }` is a block, 04 rule E0451 */
OrExpr      ::= AndExpr ("||" AndExpr)*
AndExpr     ::= CmpExpr ("&&" CmpExpr)*
CmpExpr     ::= AddExpr (("==" | "!=" | "<" | "<=" | ">" | ">=") AddExpr)?
AddExpr     ::= MulExpr (("+" | "-") MulExpr)*
MulExpr     ::= UnaryExpr (("*" | "/" | "%") UnaryExpr)*
UnaryExpr   ::= ("!" | "-")? PostfixExpr
PostfixExpr ::= Primary ("." Ident ("::" TypeArgs)? Args? | "." IntLit | Args | "[" Expr "]" | "?")*
Args        ::= "(" (Expr ("," Expr)* ","?)? ")"
Primary     ::= Literal
                | Ident ("::" TypeArgs)? /* turbofish: f::<T>(x) */
                | "(" Expr ("," Expr)* ")" /* parenthesised or tuple */
                | "(" ")" /* unit */
                | "[" (Expr ("," Expr)* ","?)? "]" /* List literal */
                | RecordExpr
                | Block
                | IfExpr
                | MatchExpr
                | Closure
                | SpawnExpr
                | TaskExpr
                | "panic" "(" Expr ")"
                | LlmCall | DecideCall | ApproveCall | AwaitSignal
                | ContractExpr /* contracts only */
RecordExpr  ::= TypePath "{" (".." Expr ",")? (FieldInit ("," FieldInit)* ","?)? "}"
FieldInit   ::= Ident (":" Expr)?
IfExpr      ::= "if" CondExpr Block ("else" (IfExpr | Block))?
MatchExpr   ::= "match" CondExpr "{" Arm ("," Arm)* ","? "}"
Arm         ::= Pattern ("if" Expr)? "=>" (Expr | Block)
Closure     ::= "fn" "(" (ClosureParam ("," ClosureParam)*)? ")" "=>" (Expr | Block)
ClosureParam ::= Ident (":" Type)?

SpawnExpr   ::= "spawn" Block
TaskExpr    ::= "task" Expr /* only inside a spawn block */

LlmCall     ::= "llm" "<" RoleLit ("," Type)? ">" "(" Expr "," Expr ")"
DecideCall  ::= "decide" "<" RoleLit ("," Type)? ">" "(" Expr ")"
ApproveCall ::= "approve" ("::" TypeArgs)? "(" Expr ")" /* only inside Workflow.run, 07 */
AwaitSignal ::= "await_signal" ("::" TypeArgs)? "(" Expr ")"
```

`<` in an expression is always less-than; an explicit type argument is the
turbofish `::<…>` (01 rule 15). `llm`, `decide`, `approve` and `via` are
keywords. `tools […]` and `Agent` are deferred from v0 (R1) and have no
grammar here.

## Patterns (04)

```
Pattern     ::= AltPattern ("|" AltPattern)*
AltPattern  ::= Literal
                | "_"
                | Ident ("@" AltPattern)? /* snake_case: binding */
                | TypePath ("(" Pattern ("," Pattern)* ")" | "{" FieldPats "}")?
                | "(" Pattern ("," Pattern)* ")"
FieldPats   ::= (FieldPat ("," FieldPat)*)? ("," "..")?
FieldPat    ::= Ident (":" Pattern)?
```

## Extern (11)

```
ExternDecl  ::= WhyLine? ("unsafe" "pure")? "extern" "fn" Ident "(" Params? ")"
                ("->" Type)? ("with" EffectSet)? "=" GoTarget NEWLINE
                /* the Item's DocLine is mandatory for an extern */
WhyLine     ::= "///" "why:" (any except newline)* NEWLINE
GoTarget    ::= "go" StrLit "." GoIdent
GoIdent     ::= ("A" … "Z") (letter | digit)*
```

## Interface files (09)

```
InterfaceFile ::= "interface" ModulePath "digest" "sha256:" Hex NEWLINE Entry* ModuleSection?
ModuleSection ::= "module-visible" "digest" "sha256:" Hex NEWLINE Entry*
Entry         ::= DocLine Signature ContractLine*
Signature     ::= "pub" ("(" "module" ")")? "opaque"?
                  (TypeSig | EnumSig | BehaviourSig | ConstSig | FnSig | ImplSig)
                  /* pub(module) entries appear only under ModuleSection, 09 rule 7 */
TypeSig       ::= "type" TypeName TypeParams? TypeBody? NEWLINE
EnumSig       ::= "enum" TypeName TypeParams? ("{" Variant ("," Variant)* "}")? NEWLINE
BehaviourSig  ::= "behaviour" TypeName TypeParams? NEWLINE (AssocTypeSig | ConstSig | FnSig)*
ImplSig       ::= "impl" (TypeName TypeArgs? "for")? Type NEWLINE (AssocType | ImplConst | FnSig)*
Hex           ::= (digit | "a" … "f")+
```

Interface files are generated by `jip check`, never hand-written; the grammar
is here so a reader of a `.jipi` can parse it. `run` lines live in `app.jip`
only (10) and are not interface entries.

## Configuration, `app.jip` (10)

```
AppFile     ::= "app" "{" AppField* "}"
AppField    ::= Ident ":" Value NEWLINE
                | Ident "{" AppField* "}" NEWLINE
                | ConfigBlock | DepsBlock | ModelsBlock | SecretsBlock | TopicsBlock
                | ExternsBlock | WorkflowBlock | RunLine | EnvBlock
Value       ::= "default"? Scalar
                | "default"? "{" (Ident ":" Value ("," Ident ":" Value)*)? "}"
Scalar      ::= StrLit | IntLit | FloatLit | "true" | "false" | DurationLit | Ident
                | IntLit "tokens" | "Money" "(" IntLit ")"
DepsBlock   ::= "deps" "{" (Ident "=" DepSource NEWLINE)* "}"
DepSource   ::= ("git" | "gomod") "(" StrLit "," "commit" ":" StrLit "," "hash" ":" StrLit ")"
ModelsBlock ::= "models" "{" (Ident "=" Provider ("," Ident "=" Provider)*)? "}"
Provider    ::= ("openai" | "anthropic" | "jev" | "ollama") "(" StrLit ")"
ConfigBlock ::= "config" "{" (ConfigField ("," ConfigField)*)? "}"
ConfigField ::= Ident ":" Type ("=" Value)?
ExternsBlock ::= "externs" "{" "allow" ":" "[" (ModulePath ("," ModulePath)*)? "]" "," "max" ":" IntLit "}"
WorkflowBlock ::= "workflow" "{" "mode" ":" "embedded" ("," "store" ":" "default"? StrLit)? "}"
RunLine     ::= "run" TypeName ("{" RunField ("," RunField)* "}")? NEWLINE
RunField    ::= "restart" ":" ("permanent" | "transient" | "temporary")
                | "max_restarts" ":" "default"? IntLit
                | "period" ":" "default"? DurationLit
                | "shutdown_timeout" ":" "default"? DurationLit
                | "port" ":" "default"? IntLit
                | "ttl" ":" "default"? DurationLit
                | "topic" ":" Ident
SecretsBlock ::= "secrets" "{" (SecretField ("," SecretField)*)? "}"
SecretField ::= Ident ":" "secret" "<" Type ">"
TopicsBlock ::= "topics" "{" (Ident ":" "Topic" "<" Type ">" NEWLINE)* "}"
EnvBlock    ::= "env" Ident "{" (FieldPath "=" Scalar ("," FieldPath "=" Scalar)*)? "}"
FieldPath   ::= Ident ("." Ident)*
```

## Scenarios (12)

```
ScenarioBody ::= (Tag* ScenarioDecl)*
ScenarioDecl ::= "scenario" StrLit "{" GivenStep* WhenStep ThenStep+ "}"
GivenStep    ::= "given" Ident "=" (Expr | AnyExpr) NEWLINE
                | "given" "(" Ident ("," Ident)+ ")" "in" Ident NEWLINE /* table rows */
                | "given" "real_time" NEWLINE
AnyExpr      ::= "any" Type ("via" Ident)? ("where" Expr)? /* `via` resolves in the module under test; `where` sees the candidate under the `given` name */
WhenStep     ::= "when" (Ident | "_") "=" Expr NEWLINE
ThenStep     ::= "then" Expr NEWLINE
Tag          ::= "@extern" NEWLINE | "@slow" NEWLINE | "@skip" "(" StrLit ")" NEWLINE
```

## Reconciliations

Names and shapes that differ between a section block and this file. The
section blocks are not wrong, only local; a parser follows this file.

- `ident` (01, 02, 04) and `Ident` (03 onward) are one nonterminal, `Ident`.
  `TypeIdent` (03) is `TypeName`. `Doc` (05) is `DocLine`. `Generics` (03)
  is `TypeParams`, which also admits an `EffectVar`.
- `FnDecl`: 05 writes `"->" Type` as mandatory and `EffectSet?` without
  `with`; 03 and this file make both optional and spell `with`. 11's
  `EffectSet` includes the `with` keyword; here `with` belongs to the
  signature and `EffectSet` is the braces.
- `Contract` (02, `inv` only) and `Contract` (05, `pre | post`) are both
  `ContractLine`, with placement fixed by the text under Contracts.
- `ImplDecl`: 03, 05 and 06 agree that `for` is optional (inherent impls,
  R3); 05 writes the implemented type as `Ident`; it is `Type`.
  06's `ImplItem` plus 03's and 05's `InvClause*` are merged into `ImplItem`.
  06's `ConstDecl` inside an impl is `ImplConst`; module-level `ConstDecl`
  (09) carries an optional type.
- `Closure`/`Param` (04) is `Closure`/`ClosureParam`; `Param` is reserved for
  function parameters, which may be `self`.
- `Stmt` and `Block` in 08 are the subset of 04 that section needed.
- 02's `Item` once listed `ScenarioDecl`; scenarios are a file kind
  (`ScenarioBody`), not an item. 12's `ModuleLine`/`UseLine` are `ModuleDecl`/
  `UseDecl`.
- 10's `Str Int Float Bool Duration` are the lexical `StrLit IntLit FloatLit
  "true" | "false" DurationLit`, and `RunSpec` is `RunLine`, which lives
  under Configuration (10), not under Interface files.
- 12's `GivenStep` and `AnyExpr` are identical to this file's.
- 09's `Visibility | Opacity` is folded into `Item` as `Visibility? "opaque"?`.
