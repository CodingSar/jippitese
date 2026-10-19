package rt

import (
	"encoding/json"
	"fmt"
)

// Every enum lowers to one struct: a Tag and one field per payload slot,
// named <Variant>_<index> or <Variant>_<field>. Option and Result are
// written by hand here; every other enum is generated in the same shape.

// Option is `Option<T>`.
type Option[T any] struct {
	Tag    uint8
	Some_0 T
}

const (
	OptionTag_None uint8 = iota
	OptionTag_Some
)

// Option_None constructs `None`.
func Option_None[T any]() Option[T] { return Option[T]{Tag: OptionTag_None} }

// Option_Some constructs `Some(v)`.
func Option_Some[T any](v T) Option[T] { return Option[T]{Tag: OptionTag_Some, Some_0: v} }

// JipVariants names the variants in declaration order.
func (Option[T]) JipVariants() []string { return []string{"None", "Some"} }

// MarshalJSON encodes `None` as null and `Some(v)` as v.
func (o Option[T]) MarshalJSON() ([]byte, error) {
	if o.Tag == OptionTag_None {
		return []byte("null"), nil
	}
	return json.Marshal(o.Some_0)
}

// UnmarshalJSON decodes null as `None` and anything else as `Some`.
func (o *Option[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*o = Option_None[T]()
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Option_Some(v)
	return nil
}

// Result is `Result<T, E>`.
type Result[T, E any] struct {
	Tag   uint8
	Ok_0  T
	Err_0 E
}

const (
	ResultTag_Ok uint8 = iota
	ResultTag_Err
)

// Result_Ok constructs `Ok(v)`.
func Result_Ok[T, E any](v T) Result[T, E] { return Result[T, E]{Tag: ResultTag_Ok, Ok_0: v} }

// Result_Err constructs `Err(e)`.
func Result_Err[T, E any](e E) Result[T, E] { return Result[T, E]{Tag: ResultTag_Err, Err_0: e} }

// JipVariants names the variants in declaration order.
func (Result[T, E]) JipVariants() []string { return []string{"Ok", "Err"} }

// MarshalJSON encodes as {"Ok": v} or {"Err": e}.
func (r Result[T, E]) MarshalJSON() ([]byte, error) {
	if r.Tag == ResultTag_Ok {
		return json.Marshal(map[string]any{"Ok": r.Ok_0})
	}
	return json.Marshal(map[string]any{"Err": r.Err_0})
}

// UnmarshalJSON decodes {"Ok": v} or {"Err": e}.
func (r *Result[T, E]) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if raw, ok := m["Ok"]; ok {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		*r = Result_Ok[T, E](v)
		return nil
	}
	if raw, ok := m["Err"]; ok {
		var e E
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		*r = Result_Err[T](e)
		return nil
	}
	return fmt.Errorf("Result: expected {\"Ok\": …} or {\"Err\": …}")
}

// ---- Option methods ----

func OptionIsSome[T any](ctx *Ctx, o Option[T]) bool { return o.Tag == OptionTag_Some }
func OptionIsNone[T any](ctx *Ctx, o Option[T]) bool { return o.Tag == OptionTag_None }
func OptionUnwrapOr[T any](ctx *Ctx, o Option[T], d T) T {
	if o.Tag == OptionTag_Some {
		return o.Some_0
	}
	return d
}
func OptionUnwrap[T any](ctx *Ctx, o Option[T]) T {
	if o.Tag == OptionTag_Some {
		return o.Some_0
	}
	panic(&PanicError{Msg: "unwrap on None"})
}
func OptionMap[T, U any](ctx *Ctx, o Option[T], f func(*Ctx, T) U) Option[U] {
	if o.Tag == OptionTag_Some {
		return Option_Some(f(ctx, o.Some_0))
	}
	return Option_None[U]()
}
func OptionAndThen[T, U any](ctx *Ctx, o Option[T], f func(*Ctx, T) Option[U]) Option[U] {
	if o.Tag == OptionTag_Some {
		return f(ctx, o.Some_0)
	}
	return Option_None[U]()
}
func OptionOkOr[T, E any](ctx *Ctx, o Option[T], e E) Result[T, E] {
	if o.Tag == OptionTag_Some {
		return Result_Ok[T, E](o.Some_0)
	}
	return Result_Err[T](e)
}

// ---- Result methods ----

func ResultIsOk[T, E any](ctx *Ctx, r Result[T, E]) bool  { return r.Tag == ResultTag_Ok }
func ResultIsErr[T, E any](ctx *Ctx, r Result[T, E]) bool { return r.Tag == ResultTag_Err }
func ResultUnwrapOr[T, E any](ctx *Ctx, r Result[T, E], d T) T {
	if r.Tag == ResultTag_Ok {
		return r.Ok_0
	}
	return d
}
func ResultUnwrap[T, E any](ctx *Ctx, r Result[T, E]) T {
	if r.Tag == ResultTag_Ok {
		return r.Ok_0
	}
	panic(&PanicError{Msg: "unwrap on Err: " + Show(r.Err_0)})
}
func ResultMap[T, E, U any](ctx *Ctx, r Result[T, E], f func(*Ctx, T) U) Result[U, E] {
	if r.Tag == ResultTag_Ok {
		return Result_Ok[U, E](f(ctx, r.Ok_0))
	}
	return Result_Err[U](r.Err_0)
}
func ResultMapErr[T, E, F any](ctx *Ctx, r Result[T, E], f func(*Ctx, E) F) Result[T, F] {
	if r.Tag == ResultTag_Ok {
		return Result_Ok[T, F](r.Ok_0)
	}
	return Result_Err[T](f(ctx, r.Err_0))
}
func ResultAndThen[T, E, U any](ctx *Ctx, r Result[T, E], f func(*Ctx, T) Result[U, E]) Result[U, E] {
	if r.Tag == ResultTag_Ok {
		return f(ctx, r.Ok_0)
	}
	return Result_Err[U](r.Err_0)
}
func ResultOk[T, E any](ctx *Ctx, r Result[T, E]) Option[T] {
	if r.Tag == ResultTag_Ok {
		return Option_Some(r.Ok_0)
	}
	return Option_None[T]()
}
func ResultErr[T, E any](ctx *Ctx, r Result[T, E]) Option[E] {
	if r.Tag == ResultTag_Err {
		return Option_Some(r.Err_0)
	}
	return Option_None[E]()
}
