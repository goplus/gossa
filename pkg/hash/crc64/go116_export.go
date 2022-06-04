// export by github.com/goplus/igop/cmd/qexp

//+build go1.16,!go1.17

package crc64

import (
	q "hash/crc64"

	"go/constant"
	"reflect"

	"github.com/goplus/igop"
)

func init() {
	igop.RegisterPackage(&igop.Package{
		Name: "crc64",
		Path: "hash/crc64",
		Deps: map[string]string{
			"errors": "errors",
			"hash":   "hash",
			"sync":   "sync",
		},
		Interfaces: map[string]reflect.Type{},
		NamedTypes: map[string]igop.NamedType{
			"Table": {reflect.TypeOf((*q.Table)(nil)).Elem(), "", ""},
		},
		AliasTypes: map[string]reflect.Type{},
		Vars:       map[string]reflect.Value{},
		Funcs: map[string]reflect.Value{
			"Checksum":  reflect.ValueOf(q.Checksum),
			"MakeTable": reflect.ValueOf(q.MakeTable),
			"New":       reflect.ValueOf(q.New),
			"Update":    reflect.ValueOf(q.Update),
		},
		TypedConsts: map[string]igop.TypedConst{},
		UntypedConsts: map[string]igop.UntypedConst{
			"ECMA": {"untyped int", constant.MakeUint64(uint64(q.ECMA))},
			"ISO":  {"untyped int", constant.MakeUint64(uint64(q.ISO))},
			"Size": {"untyped int", constant.MakeInt64(int64(q.Size))},
		},
	})
}
