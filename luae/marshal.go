package luae

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v3"
)

type Marshaler interface {
	MarshalLua() (lua.LValue, error)
}
type Unmarshaler interface {
	UnmarshalLua(vm *lua.LState, value lua.LValue) error
}

func unpackTable(vm *lua.LState, value *lua.LTable, forcearr bool) (arr []lua.LValue, mv map[string]lua.LValue, err error) {
	keys := []lua.LValue{}
	values := []lua.LValue{}
	next := lua.LNumber(1)
	arrok := true
	value.ForEach(func(key, val lua.LValue) {
		if arrok {
			if num, ok := key.(lua.LNumber); ok {
				arrok = next == num
				next++
			} else {
				arrok = false
			}
		}
		keys = append(keys, key)
		values = append(values, val)
	})

	// If we can make a list, do so
	if arrok && len(keys) != 0 {
		return values, nil, nil
	}
	if forcearr {
		return nil, nil, errors.New("expected array")
	}

	// Otherwise, make a map
	mv = map[string]lua.LValue{}
	for i, key := range keys {
		var k string
		if err = UnmarshalLua(vm, key, &k); err != nil {
			err = fmt.Errorf("table key is not convertible to string: %w", err)
			return
		}
		mv[k] = values[i]
	}
	return
}

var nilErr error = nil

func NewFunc(f func(L *lua.LState) int) *lua.LFunction {
	return &lua.LFunction{
		IsG:       true,
		Env:       &lua.LTable{},
		GFunction: f,
		Upvalues:  []*lua.Upvalue{},
	}
}

func toLuaFunc(fn reflect.Value) *lua.LFunction {
	if g, ok := fn.Interface().(func(*lua.LState) int); ok {
		return NewFunc(g)
	}
	if basic, ok := fn.Interface().(func() error); ok {
		return NewFunc(func(L *lua.LState) int {
			if e := basic(); e != nil {
				L.RaiseError(e.Error())
			}
			return 0
		})
	}

	ty := fn.Type()
	hasErr := ty.NumOut() > 0 && ty.Out(ty.NumOut()-1) == reflect.TypeOf((*error)(nil)).Elem()
	hasVM := ty.NumIn() > 0 && ty.In(0) == reflect.TypeOf((*lua.LState)(nil))

	return NewFunc(func(L *lua.LState) int {
		args := make([]reflect.Value, fn.Type().NumIn())

		argi := 0

		if hasVM {
			args[0] = reflect.ValueOf(L)
			argi++
		}

		argn := 0
		for i := argi; i < len(args); i++ {
			argn++
			v := L.Get(argn)
			if v.Type() == lua.LTNil {
				args[i] = reflect.Zero(ty.In(i))
				continue
			}
			x := reflect.New(ty.In(i))
			if e := UnmarshalLua(L, v, x.Interface()); e != nil {
				L.RaiseError("error unmarshalling argument %d: %v", i, e)
				return 0
			}
			args[i] = x.Elem()
		}

		results := fn.Call(args)
		if hasErr {
			if err, ok := results[len(results)-1].Interface().(error); ok {
				if err != nil {
					L.RaiseError(err.Error())
					return 0
				}
			}
			results = results[:len(results)-1]
		}

		for _, v := range results {
			if vv, e := MarshalLua(v.Interface()); e != nil {
				L.RaiseError("error marshalling return value: %v", e)
				return 0
			} else {
				L.Push(vv)
			}
		}
		return len(results)
	})
}
func toGoFunc(vm *lua.LState, fn *lua.LFunction, ty reflect.Type) reflect.Value {
	outN := ty.NumOut()
	outR := outN
	hasErr := false
	if outN > 0 {
		if ty.Out(outN-1) == reflect.TypeOf((*error)(nil)).Elem() {
			hasErr = true
			outR = outN - 1
		}
	}

	return reflect.MakeFunc(ty, func(args []reflect.Value) (results []reflect.Value) {
		results = make([]reflect.Value, outN)
		for i := range results {
			results[i] = reflect.New(ty.Out(i)).Elem()
		}
		defer func() {
			if hasErr {
				results[outR] = reflect.ValueOf(nilErr)
				if e := recover(); e != nil {
					if err, ok := e.(error); ok {
						results[outR] = reflect.ValueOf(err)
					} else {
						results[outR] = reflect.ValueOf(fmt.Errorf("panic: %v", e))
					}
				}
			}
		}()

		vm.Push(fn)
		for _, arg := range args {
			v, e := MarshalLua(arg.Interface())
			if e != nil {
				panic(e)
			}
			vm.Push(v)
		}

		vm.Call(len(args), outR)

		for i := 0; i < outR; i++ {
			v := vm.Get(-outR + i)
			if e := UnmarshalLua(vm, v, results[i].Addr().Interface()); e != nil {
				panic(e)
			}
		}
		return
	})
}

// TODO: Userdata, proper tokenization

// MarshalLua converts a Go value to a Lua value.
func MarshalLua(value any) (lua.LValue, error) {
	switch value := value.(type) {
	case nil:
		return lua.LNil, nil
	case Marshaler:
		return value.MarshalLua()
	case lua.LValue:
		return value, nil
	case bool:
		if value {
			return lua.LTrue, nil
		}
		return lua.LFalse, nil
	case uint64, uint32, uint16, uint8, uint:
		ui := reflect.ValueOf(value).Uint()
		return lua.LNumber(ui), nil
	case int64, int32, int16, int8, int:
		i := reflect.ValueOf(value).Int()
		return lua.LNumber(i), nil
	case float32:
		return lua.LNumber(value), nil
	case float64:
		return lua.LNumber(value), nil
	case string:
		return lua.LString(value), nil
	case lua.LGFunction:
		return NewFunc(value), nil
	case json.RawMessage:
		var v any
		if e := json.Unmarshal(value, &v); e != nil {
			return nil, e
		}
		return MarshalLua(v)
	case *yaml.Node:
		var v any
		if e := value.Decode(&v); e != nil {
			return nil, e
		}
		return MarshalLua(v)
	case encoding.TextMarshaler:
		txt, e := value.MarshalText()
		if e != nil {
			return nil, e
		}
		return lua.LString(txt), nil
	case json.Marshaler:
		js, e := value.MarshalJSON()
		if e != nil {
			return nil, e
		}
		return MarshalLua(js)
	case DynTable:
		tbl := &lua.LTable{}
		tbl.RawSetString("__index", NewFunc(func(L *lua.LState) int {
			key := L.ToString(2)
			v, ok := value.Get(L, key)
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			vv, e := MarshalLua(v)
			if e != nil {
				L.RaiseError(e.Error())
				return 0
			}
			L.Push(vv)
			return 1
		}))
		tbl.RawSetString("__newindex", NewFunc(func(L *lua.LState) int {
			key := L.ToString(2)
			v, e := MarshalLua(L.Get(3))
			if e != nil {
				L.RaiseError(e.Error())
				return 0
			}
			if e = value.Set(L, key, v); e != nil {
				L.RaiseError(e.Error())
				return 0
			}
			return 0
		}))
		tbl.Metatable = tbl
		return tbl, nil
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Func:
		return toLuaFunc(v), nil
	case reflect.Ptr:
		if v.IsNil() {
			return lua.LNil, nil
		}
		return MarshalLua(v.Elem().Interface())
	case reflect.Array, reflect.Slice:
		tbl := &lua.LTable{}
		for i := 0; i < v.Len(); i++ {
			v, e := MarshalLua(v.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			tbl.RawSetInt(i+1, v)
		}
		return tbl, nil
	case reflect.Map:
		tbl := &lua.LTable{}
		for _, k := range v.MapKeys() {
			v, e := MarshalLua(v.MapIndex(k).Interface())
			if e != nil {
				return nil, e
			}
			kv, e := MarshalLua(k.Interface())
			if e != nil {
				return nil, e
			}
			tbl.RawSet(kv, v)
		}
		return tbl, nil
	case reflect.Struct:
		js, e := json.Marshal(value)
		if e == nil {
			return MarshalLua(json.RawMessage(js))
		}
	}
	return lua.LNil, fmt.Errorf("cannot marshal %T", value)
}

// UnmarshalLua converts a Lua value to a Go value.
func UnmarshalLua(vm *lua.LState, value lua.LValue, target any) (err error) {
	switch v := target.(type) {
	case nil:
		return nil
	case *lua.LValue:
		*v = value
		return nil
	case *lua.LState:
		*v = *vm
		return nil
	case **lua.LTable:
		if value.Type() == lua.LTNil {
			*v = nil
			return nil
		}
		if value.Type() != lua.LTTable {
			return fmt.Errorf("expected table, got %s", value.Type())
		}
		*v = value.(*lua.LTable)
		return nil
	case **lua.LFunction:
		if value.Type() == lua.LTNil {
			*v = nil
			return nil
		}
		if value.Type() != lua.LTFunction {
			return fmt.Errorf("expected function, got %s", value.Type())
		}
		*v = value.(*lua.LFunction)
		return nil
	case *any:
		switch value.Type() {
		case lua.LTString:
			*v = string(value.(lua.LString))
			return nil
		case lua.LTNumber:
			*v = float64(value.(lua.LNumber))
			return nil
		case lua.LTBool:
			*v = value == lua.LTrue
			return nil
		case lua.LTNil:
			*v = nil
			return nil
		case lua.LTTable:
			arr, mv, e := unpackTable(vm, value.(*lua.LTable), false)
			if e != nil {
				return e
			}
			if len(arr) != 0 {
				aarr := make([]any, len(arr))
				for i, v := range arr {
					if e := UnmarshalLua(vm, v, &aarr[i]); e != nil {
						return e
					}
				}
				*v = aarr
			} else {
				amap := make(map[string]any, len(mv))
				for k, v := range mv {
					var av any
					if e := UnmarshalLua(vm, v, &av); e != nil {
						continue
					}
					amap[k] = av
				}
				*v = amap
			}
			return nil
		default:
			*v = value.String()
			return nil
		}
	case Unmarshaler:
		return v.UnmarshalLua(vm, value)
	case *bool:
		if value.Type() == lua.LTBool {
			*v = value.(lua.LBool) == lua.LTrue
			return nil
		}
		if value.Type() == lua.LTNumber {
			*v = value.(lua.LNumber) != 0
			return nil
		}
		if value.Type() == lua.LTString {
			*v = value.(lua.LString) != ""
			return nil
		}
		*v = value != lua.LNil
		return nil
	case *int, *int8, *int16, *int32, *int64:
		var i float64
		if e := UnmarshalLua(vm, value, &i); e != nil {
			return e
		}
		reflect.ValueOf(v).Elem().SetInt(int64(i))
		return nil
	case *uint, *uint8, *uint16, *uint32, *uint64:
		var i float64
		if e := UnmarshalLua(vm, value, &i); e != nil {
			return e
		}
		reflect.ValueOf(v).Elem().SetUint(uint64(i))
		return nil
	case *float32:
		var f float64
		if e := UnmarshalLua(vm, value, &f); e != nil {
			return e
		}
		*v = float32(f)
		return nil
	case *float64:
		if value.Type() == lua.LTBool {
			if value == lua.LTrue {
				*v = 1
			} else {
				*v = 0
			}
			return nil
		}
		if value.Type() == lua.LTNumber {
			*v = float64(value.(lua.LNumber))
			return nil
		}
		if value.Type() == lua.LTString {
			f, e := strconv.ParseFloat(string(value.(lua.LString)), 64)
			if e != nil {
				return e
			}
			*v = f
			return nil
		}
		return errors.New("expected number")
	case *string:
		switch value.Type() {
		case lua.LTString:
			*v = string(value.(lua.LString))
			return nil
		case lua.LTNumber:
			fp := float64(value.(lua.LNumber))
			if float64(int64(fp)) == fp {
				*v = strconv.FormatInt(int64(fp), 10)
			} else {
				*v = strconv.FormatFloat(fp, 'f', -1, 64)
			}
			return nil
		case lua.LTBool:
			if value == lua.LTrue {
				*v = "true"
			} else {
				*v = "false"
			}
			return nil
		case lua.LTNil:
			*v = "null"
			return nil
		case lua.LTTable:
			var js json.RawMessage
			if e := UnmarshalLua(vm, value, &js); e != nil {
				return e
			}
			*v = string(js)
			return nil
		default:
			*v = value.String()
			return nil
		}
	case *json.RawMessage:
		switch value.Type() {
		case lua.LTString:
			*v, err = json.Marshal(value.(lua.LString))
			return
		case lua.LTNumber:
			fp := float64(value.(lua.LNumber))
			var j any
			if float64(int64(fp)) == fp {
				j = int64(fp)
			} else {
				j = fp
			}
			*v, err = json.Marshal(j)
			return
		case lua.LTBool:
			*v, err = json.Marshal(value == lua.LTrue)
			return
		case lua.LTNil:
			*v = []byte("null")
			return nil
		case lua.LTTable:
			arr, mv, e := unpackTable(vm, value.(*lua.LTable), false)
			if e != nil {
				return e
			}
			if len(arr) != 0 {
				j := make([]json.RawMessage, len(arr))
				for i, v := range arr {
					if e := UnmarshalLua(vm, v, &j[i]); e != nil {
						return e
					}
				}
				*v, err = json.Marshal(j)
			} else {
				j := make(map[string]json.RawMessage, len(mv))
				for k, v := range mv {
					var jv json.RawMessage
					if e := UnmarshalLua(vm, v, &jv); e != nil {
						continue
					}
					j[k] = jv
				}
				*v, err = json.Marshal(j)
			}
			return
		default:
			return fmt.Errorf("cannot convert %s to json", value.Type())
		}
	case *yaml.Node:
		var js json.RawMessage
		if e := UnmarshalLua(vm, value, &js); e != nil {
			return e
		}
		return yaml.Unmarshal(js, v)
	case encoding.TextUnmarshaler:
		var txt string
		if err = UnmarshalLua(vm, value, &txt); err != nil {
			return
		}
		return v.UnmarshalText([]byte(txt))
	case json.Unmarshaler:
		var js json.RawMessage
		if e := UnmarshalLua(vm, value, &js); e != nil {
			return e
		}
		return v.UnmarshalJSON(js)
	}

	// We should have a pointer to a value by now
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("cannot unmarshal to %T", target)
	}
	v = v.Elem()

	// If target is a pointer to pointer
	if v.Kind() == reflect.Ptr {
		// If we have a nil value, set to nil
		if value.Type() == lua.LTNil {
			v.Set(reflect.Zero(v.Type()))
			return nil
		}

		// Recurse
		return UnmarshalLua(vm, value, v.Elem())
	}

	switch v.Kind() {
	case reflect.Func:
		if value.Type() != lua.LTFunction {
			return fmt.Errorf("expected function, got %s", value.Type())
		}
		v.Set(toGoFunc(vm, value.(*lua.LFunction), v.Type()))
		return nil
	case reflect.Map:
		if value.Type() != lua.LTTable {
			return fmt.Errorf("expected table, got %s", value.Type())
		}
		// Key type must be a string
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("map key type must be string, got %s", v.Type().Key())
		}
		etype := v.Type().Elem()
		value.(*lua.LTable).ForEach(func(key, val lua.LValue) {
			if err != nil {
				return
			}

			var ks string
			if err = UnmarshalLua(vm, key, &ks); err != nil {
				return
			}

			ev := reflect.New(etype)
			if err = UnmarshalLua(vm, val, ev.Interface()); err != nil {
				return
			}
			v.SetMapIndex(reflect.ValueOf(ks), ev.Elem())
		})
		return
	case reflect.Slice, reflect.Array:
		if value.Type() != lua.LTTable {
			return fmt.Errorf("expected table, got %s", value.Type())
		}
		arr, _, e := unpackTable(vm, value.(*lua.LTable), true)
		if e != nil {
			return e
		}

		if v.Kind() == reflect.Array {
			if len(arr) != v.Len() {
				return fmt.Errorf("array length mismatch: expected %d, got %d", v.Len(), len(arr))
			}
		} else {
			v.Set(reflect.MakeSlice(v.Type(), len(arr), len(arr)))
		}

		for i, val := range arr {
			ev := v.Index(i)
			if err = UnmarshalLua(vm, val, ev.Addr().Interface()); err != nil {
				return
			}
		}
		return
	case reflect.Struct:
		var js json.RawMessage
		if e := UnmarshalLua(vm, value, &js); e != nil {
			return e
		}
		e := json.Unmarshal(js, target)
		if e == nil {
			return nil
		}
	default:
	}
	return fmt.Errorf("cannot unmarshal to %T", target)
}

// EvalLua evaluates Lua code and unmashals the result to a Go value.
func addReturn(s *string) {
	if strings.Contains(*s, "return ") {
		return
	}
	*s = "return " + *s
}
func EvalLua(vm *lua.LState, code string, target any) (err error) {
	sp := vm.GetTop()
	defer vm.SetTop(sp)

	if target == nil {
		return vm.DoString(code)
	}

	code = strings.ReplaceAll(code, "||", "or") // TODO: need proper tokenizer for this
	code = strings.ReplaceAll(code, "&&", "and")
	code = strings.ReplaceAll(code, "!=", "~=")
	code = strings.ReplaceAll(code, "!", "not ")

	if strings.Contains(code, ";") || strings.Contains(code, "\n") {
		code = strings.ReplaceAll(code, "\r\n", "\n")
		code = strings.ReplaceAll(code, ";", "\n") // TODO: need proper tokenizer for this
		code = strings.TrimSpace(code)
		lines := strings.Split(code, "\n")
		addReturn(&lines[len(lines)-1])
		code = strings.Join(lines, "\n")
	} else {
		addReturn(&code)
	}

	if err = vm.DoString(code); err != nil {
		return
	}
	return UnmarshalLua(vm, vm.Get(-1), target)
}

// Dynamic table.
type DynTable interface {
	Get(L *lua.LState, key string) (value any, ok bool)
	Set(L *lua.LState, key string, value lua.LValue) (err error)
}
