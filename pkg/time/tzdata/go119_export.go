// export by github.com/goplus/igop/cmd/qexp

//go:build go1.19
// +build go1.19

package tzdata

import (
	_ "time/tzdata"

	"github.com/goplus/igop"
)

func init() {
	igop.RegisterPackage(&igop.Package{
		Name: "tzdata",
		Path: "time/tzdata",
		Deps: map[string]string{
			"errors":  "errors",
			"syscall": "syscall",
			"unsafe":  "unsafe",
		},
	})
}
