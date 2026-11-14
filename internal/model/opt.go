package model

import (
	"encoding/json"
	"fmt"
)

// Opt is an optional value. The zero value means "not reported by the
// source", which is different from a reported zero: row 0 is a valid row.
type Opt[T any] struct {
	V     T
	Valid bool
}

// Some returns a set Opt holding v.
func Some[T any](v T) Opt[T] { return Opt[T]{V: v, Valid: true} }

// None returns an unset Opt.
func None[T any]() Opt[T] { return Opt[T]{} }

// Get returns the value and whether it is set.
func (o Opt[T]) Get() (T, bool) { return o.V, o.Valid }

// Or returns the value, or def when unset.
func (o Opt[T]) Or(def T) T {
	if o.Valid {
		return o.V
	}
	return def
}

// IsZero reports whether the value is unset. encoding/json uses it for the
// omitzero tag option.
func (o Opt[T]) IsZero() bool { return !o.Valid }

// String formats the value, or "-" when unset.
func (o Opt[T]) String() string {
	if !o.Valid {
		return "-"
	}
	return fmt.Sprint(o.V)
}

// MarshalJSON encodes an unset value as null.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.V)
}

// UnmarshalJSON decodes null as unset.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*o = Opt[T]{}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Opt[T]{V: v, Valid: true}
	return nil
}
