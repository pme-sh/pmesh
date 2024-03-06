package lyml

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"get.pme.sh/pmesh/luae"
	"github.com/samber/lo"
	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v3"
)

func withWd(wd string, f func()) {
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(wd)
	f()
}

type specialNode interface {
	eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) error
}

type mismatchErr struct {
	node     *yaml.Node
	expected string
}

func (m mismatchErr) Error() string {
	var res any
	m.node.Decode(&res)
	yml, _ := json.Marshal(res)
	return fmt.Sprintf("expected %v, got: %v", m.expected, string(yml))
}
func newMismatchErr(node *yaml.Node, expected yaml.Kind) error {
	if node.Kind == yaml.ScalarNode {
		return mismatchErr{node, "string"}
	} else if node.Kind == yaml.MappingNode {
		return mismatchErr{node, "mapping"}
	} else if node.Kind == yaml.SequenceNode {
		return mismatchErr{node, "sequence"}
	} else {
		return mismatchErr{node, fmt.Sprintf("kind %v", expected)}
	}
}

// yaml utils
type trapnode struct {
	*yaml.Node
}

func (t *trapnode) UnmarshalYAML(value *yaml.Node) (err error) {
	t.Node = value
	return
}
func parse(in []byte) (res *yaml.Node, err error) {
	var tn trapnode
	err = yaml.Unmarshal(in, &tn)
	return tn.Node, err
}

func clone(node *yaml.Node) *yaml.Node {
	n := *node
	n.Content = make([]*yaml.Node, len(node.Content))
	for i, c := range node.Content {
		n.Content[i] = clone(c)
	}
	return &n
}
func mixin(out *[]*yaml.Node, in *yaml.Node, kind yaml.Kind) error {
	// null can mix into anything as noop.
	if in.Tag == "!!null" {
		return nil
	}
	if in.Kind != kind {
		// Allow mixing from both T and T[] into T[].
		if kind == yaml.SequenceNode {
			*out = append(*out, in)
			return nil
		}
		return newMismatchErr(in, kind)
	}
	*out = append(*out, in.Content...)
	return nil
}

/*
> a:
>   one: 1
>   ${if match("hiDad", "hiD.d")}:
>     two: 2
>     three: 3
> b:
>   - 1
>   - ${if 5==5}:
>       - 2
>       - 3
*/
type conditionalNode struct {
	Cond string
	Node *yaml.Node
}

func (c conditionalNode) eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) error {
	var cond bool
	if err := luae.EvalLua(vm, c.Cond, &cond); err != nil {
		return err
	}
	if cond {
		return mixin(result, c.Node, kind)
	}
	return nil
}

/*
> ${match hello}:
>   kek:
>     - 1
>   oyy:
>     - 2
>   ".*orld":
>     - 3
>     - 4
*/
type matchNode struct {
	Cond string
	Node *yaml.Node
}

func (c matchNode) eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) error {
	if c.Node.Kind != yaml.MappingNode {
		return newMismatchErr(c.Node, yaml.MappingNode)
	}

	var cond string
	if err := luae.EvalLua(vm, c.Cond, &cond); err != nil {
		return err
	}

	for i := 0; i < len(c.Node.Content); i += 2 {
		if c.Node.Content[i].Kind != yaml.ScalarNode {
			return newMismatchErr(c.Node.Content[i], yaml.ScalarNode)
		}
		rgx, err := regexp.Compile(c.Node.Content[i].Value)
		if err != nil {
			return err
		}
		if rgx.MatchString(cond) {
			return mixin(result, c.Node.Content[i+1], kind)
		}
	}
	return nil
}

/*
> ${each table}
> ${each table as k}
> ${each table as k, v}
>   - $(k): $(v)
*/
type eachNode struct {
	Directive string
	Node      *yaml.Node
}

func (c eachNode) eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) (err error) {
	var tname, kname, vname string
	if before, after, ok := strings.Cut(c.Directive, " as "); ok {
		tname = strings.TrimSpace(before)
		if kname, vname, ok = strings.Cut(after, ","); !ok {
			kname = after
		}
		kname = strings.TrimSpace(kname)
		vname = strings.TrimSpace(vname)
	} else {
		tname = strings.TrimSpace(c.Directive)
	}

	var t *lua.LTable
	if err = luae.EvalLua(vm, tname, &t); err != nil {
		return
	}
	if t == nil {
		return nil
	}

	scope := uniqueScope{l: vm}
	scope.Enter()
	defer scope.Exit()

	t.ForEach(func(k, v lua.LValue) {
		if err != nil {
			return
		}
		if kname != "" {
			if k.Type() == lua.LTNumber {
				k = lua.LNumber(k.(lua.LNumber) - 1)
			}
			scope.env.RawSetString(kname, k)
		}
		if vname != "" {
			scope.env.RawSetString(vname, v)
		}
		var node *yaml.Node
		node, err = evaluate(vm, clone(c.Node))
		if err != nil {
			return
		}
		err = mixin(result, node, kind)
	})
	return
}

/*
> $:
>   a: 9
>   b: 8
> b:
>   - $: |
>       a = a + 1
>       b = b - 1
>       print(a,b)
>
> also defines push or set for sequences and mappings
*/
type localsNode struct {
	Node *yaml.Node
}

func (c localsNode) eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) (err error) {
	if c.Node.Kind == yaml.MappingNode {
		for i := 0; i < len(c.Node.Content); i += 2 {
			// Decode the key and value as generic type.
			var ak, av any
			if err = c.Node.Content[i].Decode(&ak); err != nil {
				return
			}
			var vv *yaml.Node
			vv, err = evaluate(vm, c.Node.Content[i+1])
			if err != nil {
				return
			}
			if err = vv.Decode(&av); err != nil {
				return
			}

			// Marshal the key and value into lua types.
			var lk, lv lua.LValue
			if lk, err = luae.MarshalLua(ak); err != nil {
				return
			}
			if lv, err = luae.MarshalLua(av); err != nil {
				return
			}

			// Set the key and value in the environment.
			vm.Env.RawSet(lk, lv)
		}
		return nil
	} else if c.Node.Kind == yaml.ScalarNode {
		var code string
		if err = c.Node.Decode(&code); err != nil {
			return
		}

		if kind == yaml.MappingNode {
			vm.Env.RawSet(lua.LString("set"), lo.Must(luae.MarshalLua(func(k, v json.RawMessage) (err error) {
				yk, err := parse(k)
				if err != nil {
					return
				}
				yv, err := parse(v)
				if err != nil {
					return
				}
				*result = append(*result, yk, yv)
				return nil
			})))
			defer vm.Env.RawSet(lua.LString("set"), lua.LNil)
		} else if kind == yaml.SequenceNode {
			vm.Env.RawSet(lua.LString("push"), lo.Must(luae.MarshalLua(func(v json.RawMessage) (err error) {
				yv, err := parse(v)
				if err != nil {
					return
				}
				*result = append(*result, yv)
				return nil
			})))
			defer vm.Env.RawSet(lua.LString("push"), lua.LNil)
		}

		return luae.EvalLua(vm, code, nil)
	}
	return mismatchErr{c.Node, "code or mapping"}
}

/*
> a:
>   one: 1
>   ${if match("hiDad", "hiD.d")}: #regex match, conditional assign
>     two: 2
>     three: 3
>   $import: ./a.yml
>   $import: ./*.yml
>   $import:
  - ./a.yml
  - ./b.yml
*/
type importNode struct {
	Path *yaml.Node
}

func (c importNode) eval(vm *lua.LState, result *[]*yaml.Node, kind yaml.Kind) (err error) {
	// Decode the path.
	//
	node, err := evaluate(vm, c.Path)
	if err != nil {
		return
	}
	var paths []string
	if node.Kind == yaml.ScalarNode {
		var path string
		if err = node.Decode(&path); err != nil {
			return
		}
		paths = []string{path}
	} else if node.Kind == yaml.SequenceNode {
		for _, p := range node.Content {
			var path string
			if err = p.Decode(&path); err != nil {
				return
			}
			paths = append(paths, path)
		}
	} else {
		return newMismatchErr(node, yaml.ScalarNode)
	}

	scope := uniqueScope{l: vm}
	scope.Enter()
	defer scope.Exit()

	// Mixin each file.
	//
	for _, path := range paths {
		files, _ := filepath.Glob(path)
		for _, file := range files {
			var data []byte
			if data, err = os.ReadFile(file); err != nil {
				return
			}

			withWd(filepath.Dir(file), func() {
				var node *yaml.Node
				node, err = unmarshal(vm, data)
				if err == nil {
					err = mixin(result, node, kind)
				}
			})
			if err != nil {
				return
			}
		}
	}
	return nil
}

// Parse a special node from a key-value pair.
func parseSpecialPair(k, v *yaml.Node) specialNode {
	if k.Kind != yaml.ScalarNode {
		return nil
	}
	if k.Value == "$" {
		return localsNode{Node: v}
	}
	if k.Value == "$import" {
		return importNode{Path: v}
	}
	if strings.HasPrefix(k.Value, "${") && strings.HasSuffix(k.Value, "}") {
		act := k.Value[2 : len(k.Value)-1]
		act = strings.TrimSpace(act)
		verb, rest, _ := strings.Cut(act, " ")
		switch verb {
		case "if":
			return conditionalNode{Cond: rest, Node: v}
		case "match":
			return matchNode{Cond: rest, Node: v}
		case "each":
			return eachNode{Directive: rest, Node: v}
		}
	}
	return nil
}

// Parse a special node from a node.
func parseSpecialNode(e *yaml.Node) specialNode {
	if e.Kind != yaml.MappingNode {
		return nil
	}
	if len(e.Content) != 2 {
		return nil
	}
	return parseSpecialPair(e.Content[0], e.Content[1])
}

// Unique environment for lua execution.
type uniqueScope struct {
	l         *lua.LState
	prev, env *lua.LTable
}

func (u *uniqueScope) Enter() {
	if u.env != nil {
		return
	}
	u.prev = u.l.Env

	env := &lua.LTable{}
	env.RawSetString("__index", u.prev)
	env.Metatable = env

	u.l.Env = env
	u.env = env
}
func (u *uniqueScope) Exit() {
	if u.env == nil {
		return
	}
	u.l.Env = u.prev
	u.env = nil
}

// Evalutes a super-node and returns the result.
func evaluate(vm *lua.LState, node *yaml.Node) (result *yaml.Node, err error) {
	// If this is a scalar node, replace all $(...) with the result of the lua execute
	if node.Kind == yaml.ScalarNode {
		// If the entire thing is a lua expression, evaluate it, replace the node with the result.
		if strings.HasPrefix(node.Value, "$(") && strings.HasSuffix(node.Value, ")") {
			script := node.Value[2 : len(node.Value)-1]
			var res json.RawMessage
			if err = luae.EvalLua(vm, script, &res); err != nil {
				return
			}
			return parse(res)
		} else {
			node.Value = evalEscape(node.Value, vm)
		}
		return node, nil
	}

	scope := uniqueScope{l: vm}
	defer scope.Exit()

	switch node.Kind {
	case yaml.SequenceNode:
		res := []*yaml.Node{}
		for _, child := range node.Content {
			s := parseSpecialNode(child)
			if s != nil {
				scope.Enter()
				if err = s.eval(vm, &res, yaml.SequenceNode); err != nil {
					return
				}
				continue
			}
			res = append(res, child)
		}
		node.Content = res
	case yaml.MappingNode:
		res := []*yaml.Node{}
		for i := 0; i < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			s := parseSpecialPair(k, v)
			if s != nil {
				scope.Enter()
				if err = s.eval(vm, &res, yaml.MappingNode); err != nil {
					return
				}
				continue
			}
			res = append(res, k, v)
		}
		node.Content = res
	}

	// Evalute all children.
	for i, child := range node.Content {
		node.Content[i], err = evaluate(vm, child)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// Unmarshals a document with super-nodes into a yaml.Node.
func unmarshal(vm *lua.LState, data []byte) (res *yaml.Node, err error) {
	node, err := parse(data)
	if err != nil {
		return
	}
	return evaluate(vm, node)
}

// Transform takes a lyml document and returns the parsed yaml.Node.
func TransformContext(ctx context.Context, lyml []byte) (res *yaml.Node, err error) {
	vm := NewContextVM(ctx)
	defer vm.Close()
	return unmarshal(vm, lyml)
}
func Transform(lyml []byte) (res *yaml.Node, err error) {
	return TransformContext(context.Background(), lyml)
}

// Render takes a lyml document and returns a valid yml document with all special nodes evaluated.
func RenderContext(ctx context.Context, lyml []byte) (res []byte, err error) {
	node, err := TransformContext(ctx, lyml)
	if err != nil {
		return
	}
	return yaml.Marshal(node)
}
func Render(lyml []byte) (res []byte, err error) {
	return RenderContext(context.Background(), lyml)
}

// Unmarshal transforms a lyml document into a yml one and then unmarshals it into the given value.
func UnmarshalContext(ctx context.Context, data []byte, v any) error {
	node, err := TransformContext(ctx, data)
	if err != nil {
		return err
	}
	if y, ok := v.(**yaml.Node); ok {
		*y = node
		return nil
	} else {
		return node.Decode(v)
	}
}
func Unmarshal(data []byte, v any) error {
	return UnmarshalContext(context.Background(), data, v)
}

// Load is unmarshal with file path.
func LoadContext(ctx context.Context, path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	withWd(filepath.Dir(path), func() {
		err = UnmarshalContext(ctx, data, v)
	})
	return err
}
func Load(path string, v any) error {
	return LoadContext(context.Background(), path, v)
}
