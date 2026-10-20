package rt

import (
	"hash/maphash"
	"math"
	"reflect"
	"strings"
)

// ---- hashing ----
//
// Hash is consistent with Equal: Equal(a, b) implies Hash(a) == Hash(b).
// The seed is per process, so hashes are never persisted or compared across
// runs, and no observable order depends on them.

var hashSeed = maphash.MakeSeed()

// jipHasher is implemented by the containers that implement jipEqualer.
type jipHasher interface{ JipHash() uint64 }

// mix is the splitmix64 finalizer.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func combine(h, x uint64) uint64 { return mix(h*31 + x) }

func hashFloat(f float64) uint64 {
	if f == 0 {
		f = 0 // -0 == +0
	}
	return mix(math.Float64bits(f))
}

// Hash returns the structural hash of a jippitese value.
func Hash(v any) uint64 {
	switch x := v.(type) {
	case nil:
		return 0
	case string:
		return maphash.String(hashSeed, x)
	case int64:
		return mix(uint64(x))
	case int:
		return mix(uint64(x))
	case rune:
		return mix(uint64(x))
	case bool:
		if x {
			return 1
		}
		return 2
	case float64:
		return hashFloat(x)
	}
	return hashValue(reflect.ValueOf(v))
}

func hashValue(v reflect.Value) uint64 {
	if v.CanInterface() {
		if h, ok := v.Interface().(jipHasher); ok {
			return h.JipHash()
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return 1
		}
		return 2
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return mix(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return mix(v.Uint())
	case reflect.Float32, reflect.Float64:
		return hashFloat(v.Float())
	case reflect.String:
		return maphash.String(hashSeed, v.String())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return maphash.Bytes(hashSeed, v.Bytes())
		}
		h := uint64(v.Len())
		for i := 0; i < v.Len(); i++ {
			h = combine(h, hashValue(v.Index(i)))
		}
		return h
	case reflect.Struct:
		if isEnum(v.Type()) {
			tag := v.Field(0).Uint()
			h := mix(tag + 1)
			name := variantNames(v)[tag]
			for i := 1; i < v.NumField(); i++ {
				if strings.HasPrefix(v.Type().Field(i).Name, name+"_") {
					h = combine(h, hashValue(v.Field(i)))
				}
			}
			return h
		}
		var h uint64
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				h = combine(h, hashValue(v.Field(i)))
			}
		}
		return h
	case reflect.Ptr, reflect.Chan, reflect.Func, reflect.Map, reflect.UnsafePointer:
		return mix(uint64(v.Pointer()))
	case reflect.Interface:
		if v.IsNil() {
			return 0
		}
		return hashValue(v.Elem())
	}
	return 0
}

// hashOf and keyEq are Hash and Equal with the common key types unboxed.
func hashOf[K any](k K) uint64 {
	switch x := any(k).(type) {
	case string:
		return maphash.String(hashSeed, x)
	case int64:
		return mix(uint64(x))
	}
	return Hash(k)
}

func keyEq[K any](a, b K) bool {
	switch x := any(a).(type) {
	case string:
		return x == any(b).(string)
	case int64:
		return x == any(b).(int64)
	}
	return Equal(a, b)
}
