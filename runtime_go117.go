//go:build !go1.18
// +build !go1.18

package igop

import (
	"runtime"
	"unsafe"
)

type funcinl struct {
	zero  uintptr // set to 0 to distinguish from _func
	entry uintptr // entry of the real (the "outermost") frame.
	name  string
	file  string
	line  int
}

func inlineFunc(entry uintptr) *funcinl {
	return &funcinl{entry: entry}
}

func isInlineFunc(f *runtime.Func) bool {
	return (*funcinl)(unsafe.Pointer(f)).zero == 0
}
