// export by github.com/goplus/igop/cmd/qexp

//+build go1.14,!go1.15

package pprof

import (
	q "runtime/pprof"

	"reflect"

	"github.com/goplus/igop"
)

func init() {
	igop.RegisterPackage(&igop.Package{
		Name: "pprof",
		Path: "runtime/pprof",
		Deps: map[string]string{
			"bufio":           "bufio",
			"bytes":           "bytes",
			"compress/gzip":   "gzip",
			"context":         "context",
			"encoding/binary": "binary",
			"errors":          "errors",
			"fmt":             "fmt",
			"io":              "io",
			"io/ioutil":       "ioutil",
			"math":            "math",
			"os":              "os",
			"runtime":         "runtime",
			"sort":            "sort",
			"strconv":         "strconv",
			"strings":         "strings",
			"sync":            "sync",
			"text/tabwriter":  "tabwriter",
			"time":            "time",
			"unsafe":          "unsafe",
		},
		Interfaces: map[string]reflect.Type{},
		NamedTypes: map[string]igop.NamedType{
			"LabelSet": {reflect.TypeOf((*q.LabelSet)(nil)).Elem(), "", ""},
			"Profile":  {reflect.TypeOf((*q.Profile)(nil)).Elem(), "", "Add,Count,Name,Remove,WriteTo"},
		},
		AliasTypes: map[string]reflect.Type{},
		Vars:       map[string]reflect.Value{},
		Funcs: map[string]reflect.Value{
			"Do":                 reflect.ValueOf(q.Do),
			"ForLabels":          reflect.ValueOf(q.ForLabels),
			"Label":              reflect.ValueOf(q.Label),
			"Labels":             reflect.ValueOf(q.Labels),
			"Lookup":             reflect.ValueOf(q.Lookup),
			"NewProfile":         reflect.ValueOf(q.NewProfile),
			"Profiles":           reflect.ValueOf(q.Profiles),
			"SetGoroutineLabels": reflect.ValueOf(q.SetGoroutineLabels),
			"StartCPUProfile":    reflect.ValueOf(q.StartCPUProfile),
			"StopCPUProfile":     reflect.ValueOf(q.StopCPUProfile),
			"WithLabels":         reflect.ValueOf(q.WithLabels),
			"WriteHeapProfile":   reflect.ValueOf(q.WriteHeapProfile),
		},
		TypedConsts:   map[string]igop.TypedConst{},
		UntypedConsts: map[string]igop.UntypedConst{},
	})
}
