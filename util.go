package steam

import (
	"runtime"
	"strings"

	"github.com/davecgh/go-spew/spew"
)

func pp(args ...interface{}) {
	pc, _, _, ok := runtime.Caller(1)
	pppc(pc, ok, args...)
}

func pp2(args ...interface{}) {
	pc, _, _, ok := runtime.Caller(2)
	pppc(pc, ok, args...)
}

func pppc(pc uintptr, ok bool, args ...interface{}) {
	if ok {
		f := runtime.FuncForPC(pc)
		fParts := strings.Split(f.Name(), ".")
		fun := fParts[len(fParts)-1]
		s := spew.Sprintf("vvvvvvvvvvvvvvv %s vvvvvvvvvvvvvvv\n", fun)
		spew.Print(s)
		spew.Dump(args...)
		spew.Println(strings.Repeat("^", len(s)-1))
	} else {
		spew.Println("vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv")
		spew.Dump(args...)
		spew.Println("^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^")
	}
}
