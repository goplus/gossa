// export by github.com/goplus/gossa/cmd/qexp

//go:build go1.18
// +build go1.18

package maphash

import (
	q "hash/maphash"

	"reflect"

	"github.com/goplus/gossa"
)

func init() {
	gossa.RegisterPackage(&gossa.Package{
		Name: "maphash",
		Path: "hash/maphash",
		Deps: map[string]string{
			"internal/unsafeheader": "unsafeheader",
			"unsafe":                "unsafe",
		},
		Interfaces: map[string]reflect.Type{},
		NamedTypes: map[string]gossa.NamedType{
			"Hash": {reflect.TypeOf((*q.Hash)(nil)).Elem(), "", "BlockSize,Reset,Seed,SetSeed,Size,Sum,Sum64,Write,WriteByte,WriteString,flush,initSeed"},
			"Seed": {reflect.TypeOf((*q.Seed)(nil)).Elem(), "", ""},
		},
		AliasTypes: map[string]reflect.Type{},
		Vars:       map[string]reflect.Value{},
		Funcs: map[string]reflect.Value{
			"Bytes":    reflect.ValueOf(q.Bytes),
			"MakeSeed": reflect.ValueOf(q.MakeSeed),
			"String":   reflect.ValueOf(q.String),
		},
		TypedConsts:   map[string]gossa.TypedConst{},
		UntypedConsts: map[string]gossa.UntypedConst{},
	})
}
