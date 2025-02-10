// export by github.com/goplus/igop/cmd/qexp

package race

import (
	q "github.com/goplus/igop/race"

	"go/constant"
	"reflect"

	"github.com/goplus/igop"
)

func init() {
	igop.RegisterPackage(&igop.Package{
		Name: "race",
		Path: "github.com/goplus/igop/race",
		Deps: map[string]string{
			"unsafe": "unsafe",
		},
		Interfaces: map[string]reflect.Type{},
		NamedTypes: map[string]reflect.Type{},
		AliasTypes: map[string]reflect.Type{},
		Vars:       map[string]reflect.Value{},
		Funcs: map[string]reflect.Value{
			"Acquire":      reflect.ValueOf(q.Acquire),
			"Disable":      reflect.ValueOf(q.Disable),
			"Enable":       reflect.ValueOf(q.Enable),
			"Errors":       reflect.ValueOf(q.Errors),
			"Read":         reflect.ValueOf(q.Read),
			"ReadRange":    reflect.ValueOf(q.ReadRange),
			"Release":      reflect.ValueOf(q.Release),
			"ReleaseMerge": reflect.ValueOf(q.ReleaseMerge),
			"Write":        reflect.ValueOf(q.Write),
			"WriteRange":   reflect.ValueOf(q.WriteRange),
		},
		TypedConsts: map[string]igop.TypedConst{},
		UntypedConsts: map[string]igop.UntypedConst{
			"Enabled": {"untyped bool", constant.MakeBool(bool(q.Enabled))},
		},
	})
}
