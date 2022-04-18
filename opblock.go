package gossa

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/goplus/reflectx"
	"github.com/visualfc/funcval"
	"golang.org/x/tools/go/ssa"
)

/*
type Op int

const (
	OpInvalid Op = iota
	// Value-defining instructions
	OpAlloc
	OpPhi
	OpCall
	OpBinOp
	OpUnOp
	OpChangeType
	OpConvert
	OpChangeInterface
	OpSliceToArrayPointer
	OpMakeInterface
	OpMakeClosure
	OpMakeMap
	OpMakeChan
	OpMakeSlice
	OpSlice
	OpFieldAddr
	OpField
	OpIndexAddr
	OpIndex
	OpLookup
	OpSelect
	OpRange
	OpNext
	OpTypeAssert
	OpExtract
	// Instructions executed for effect
	OpJump
	OpIf
	OpReturn
	OpRunDefers
	OpPanic
	OpGo
	OpDefer
	OpSend
	OpStore
	OpMapUpdate
	OpDebugRef
)
*/

type kind int

func (k kind) isStatic() bool {
	return k == kindConst || k == kindGlobal || k == kindFunction
}

const (
	kindInvalid kind = iota
	kindConst
	kindGlobal
	kindFunction
)

type Register uint32

func (i Register) Set(fr *frame, v value) {
	if i>>24 == 0 {
		fr.stack[i] = v
	} else {
		fr.interp.stack[i&0xffffff] = v
	}
}

func (i Register) Get(fr *frame) value {
	if i>>24 == 0 {
		return fr.stack[i]
	}
	return fr.interp.stack[i&0xffffff]
}

func (i Register) Kind() kind {
	return kind(i >> 24)
}

func (i Register) Index() int {
	return int(i & 0xffffff)
}

type Function struct {
	Interp           *Interp
	Fn               *ssa.Function          // ssa function
	Main             *ssa.BasicBlock        // Fn.Blocks[0]
	Instrs           []func(fr *frame)      // main instrs
	Recover          []func(fr *frame)      // recover instrs
	Blocks           []int                  // block offset
	ssaInstrs        []ssa.Instruction      // org ssa instr
	stackIndex       map[ssa.Value]Register // data stack index
	mapUnderscoreKey map[types.Type]bool
	pool             *sync.Pool
	nstack           int
	narg             int
	nenv             int
	used             int32 // function used count
	cached           int32 // enable cached by pool
}

func (p *Function) initPool() {
	p.pool = &sync.Pool{}
	p.pool.New = func() interface{} {
		fr := &frame{interp: p.Interp, pfn: p, block: p.Main}
		fr.stack = make([]value, p.nstack, p.nstack)
		return fr
	}
}

func (p *Function) allocFrame(caller *frame) *frame {
	var fr *frame
	if atomic.LoadInt32(&p.cached) == 1 {
		fr = p.pool.Get().(*frame)
	} else {
		if atomic.AddInt32(&p.used, 1) > int32(p.Interp.ctx.callForPool) {
			atomic.StoreInt32(&p.cached, 1)
		}
		fr = &frame{interp: p.Interp, pfn: p, block: p.Main}
		fr.stack = make([]value, p.nstack, p.nstack)
	}
	fr.caller = caller
	if caller != nil {
		fr.deferid = caller.deferid
	}
	if fr.cached {
		fr.block = p.Main
		fr.defers = nil
		fr.panicking = nil
		fr.pc = 0
		fr.pred = 0
	}
	return fr
}

func (p *Function) deleteFrame(fr *frame) {
	// release closure env for gc
	if atomic.LoadInt32(&p.cached) == 1 {
		fr.cached = true
		p.pool.Put(fr)
	}
	fr = nil
}

func (p *Function) InstrForPC(pc int) ssa.Instruction {
	if pc >= 0 && pc < len(p.ssaInstrs) {
		return p.ssaInstrs[pc]
	}
	return nil
}

func (p *Function) PosForPC(pc int) token.Pos {
	if instr := p.InstrForPC(pc); instr != nil {
		return instr.Pos()
	}
	return token.NoPos
}

func (p *Function) regIndex3(v ssa.Value) (ir Register, ik kind, iv value) {
	ir = p.regIndex(v)
	ik = ir.Kind()
	if ik != 0 {
		iv = p.Interp.stack[ir.Index()]
	}
	return
}

func (p *Function) regIndex(v ssa.Value) (reg Register) {
	if i, ok := p.Interp.stackIndex[v]; ok {
		return i
	}
	if i, ok := p.stackIndex[v]; ok {
		return i
	}
	var vs interface{}
	var vk kind
	switch v := v.(type) {
	case *ssa.Const:
		vs = constToValue(p.Interp, v)
		vk = kindConst
	case *ssa.Global:
		vs, _ = globalToValue(p.Interp, v)
		vk = kindGlobal
	case *ssa.Function:
		if v.Blocks != nil {
			typ := p.Interp.preToType(v.Type())
			pfn := p.Interp.loadFunction(v)
			vs = p.Interp.makeFunc(typ, pfn, nil).Interface()
			vk = kindFunction
		}
	}
	if vk != 0 {
		reg = Register(len(p.Interp.stack) | int(vk<<24))
		p.Interp.stack = append(p.Interp.stack, vs)
		p.Interp.stackIndex[v] = reg
	} else {
		reg = Register(p.nstack)
		p.nstack++
		p.stackIndex[v] = reg
	}
	return
}

func findExternFunc(interp *Interp, fn *ssa.Function) (ext reflect.Value, ok bool) {
	fnName := fn.String()
	if fnName == "os.Exit" {
		return reflect.ValueOf(func(code int) {
			if interp.exited {
				os.Exit(code)
			} else {
				panic(exitPanic(code))
			}
		}), true
	}
	// check override func
	ext, ok = interp.ctx.override[fnName]
	if ok {
		return
	}
	// check extern func
	ext, ok = externValues[fnName]
	if ok {
		return
	}
	if fn.Pkg != nil {
		if recv := fn.Signature.Recv(); recv == nil {
			if pkg, found := interp.installed(fn.Pkg.Pkg.Path()); found {
				ext, ok = pkg.Funcs[fn.Name()]
			}
		} else if typ, found := interp.loader.LookupReflect(recv.Type()); found {
			if m, found := reflectx.MethodByName(typ, fn.Name()); found {
				ext, ok = m.Func, true
			}
		}
	}
	return
}

func makeInstr(interp *Interp, pfn *Function, instr ssa.Instruction) func(fr *frame) {
	switch instr := instr.(type) {
	case *ssa.Alloc:
		if instr.Heap {
			typ := interp.preToType(instr.Type()).Elem()
			ir := pfn.regIndex(instr)
			return func(fr *frame) {
				fr.setReg(ir, reflect.New(typ).Interface())
			}
		} else {
			typ := interp.preToType(instr.Type()).Elem()
			elem := reflect.New(typ).Elem()
			ir := pfn.regIndex(instr)
			return func(fr *frame) {
				if v := fr.reg(ir); v != nil {
					SetValue(reflect.ValueOf(v).Elem(), elem)
				} else {
					fr.setReg(ir, reflect.New(typ).Interface())
				}
			}
		}
	case *ssa.Phi:
		ir := pfn.regIndex(instr)
		ie := make([]Register, len(instr.Edges))
		for i, v := range instr.Edges {
			ie[i] = pfn.regIndex(v)
		}
		return func(fr *frame) {
			for i, pred := range instr.Block().Preds {
				if fr.pred == pred.Index {
					fr.setReg(ir, fr.reg(ie[i]))
					break
				}
			}
		}
	case *ssa.Call:
		return makeCallInstr(pfn, interp, instr, &instr.Call)
	case *ssa.BinOp:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		iy := pfn.regIndex(instr.Y)
		switch instr.Op {
		case token.ADD:
			return func(fr *frame) {
				fr.setReg(ir, opADD(fr.reg(ix), fr.reg(iy)))
			}
		case token.SUB:
			return func(fr *frame) {
				fr.setReg(ir, opSUB(fr.reg(ix), fr.reg(iy)))
			}
		case token.MUL:
			return func(fr *frame) {
				fr.setReg(ir, opMUL(fr.reg(ix), fr.reg(iy)))
			}
		case token.QUO:
			return func(fr *frame) {
				fr.setReg(ir, opQuo(fr.reg(ix), fr.reg(iy)))
			}
		case token.REM:
			return func(fr *frame) {
				fr.setReg(ir, opREM(fr.reg(ix), fr.reg(iy)))
			}
		case token.AND:
			return func(fr *frame) {
				fr.setReg(ir, opAND(fr.reg(ix), fr.reg(iy)))
			}
		case token.OR:
			return func(fr *frame) {
				fr.setReg(ir, opOR(fr.reg(ix), fr.reg(iy)))
			}
		case token.XOR:
			return func(fr *frame) {
				fr.setReg(ir, opXOR(fr.reg(ix), fr.reg(iy)))
			}
		case token.AND_NOT:
			return func(fr *frame) {
				fr.setReg(ir, opANDNOT(fr.reg(ix), fr.reg(iy)))
			}
		case token.SHL:
			return func(fr *frame) {
				fr.setReg(ir, opSHL(fr.reg(ix), fr.reg(iy)))
			}
		case token.SHR:
			return func(fr *frame) {
				fr.setReg(ir, opSHR(fr.reg(ix), fr.reg(iy)))
			}
		case token.LSS:
			return func(fr *frame) {
				fr.setReg(ir, opLSS(fr.reg(ix), fr.reg(iy)))
			}
		case token.LEQ:
			return func(fr *frame) {
				fr.setReg(ir, opLEQ(fr.reg(ix), fr.reg(iy)))
			}
		case token.EQL:
			return func(fr *frame) {
				fr.setReg(ir, opEQL(instr, fr.reg(ix), fr.reg(iy)))
			}
		case token.NEQ:
			return func(fr *frame) {
				fr.setReg(ir, !opEQL(instr, fr.reg(ix), fr.reg(iy)))
			}
		case token.GTR:
			return func(fr *frame) {
				fr.setReg(ir, opGTR(fr.reg(ix), fr.reg(iy)))
			}
		case token.GEQ:
			return func(fr *frame) {
				fr.setReg(ir, opGEQ(fr.reg(ix), fr.reg(iy)))
			}
		default:
			panic(fmt.Errorf("unreachable %v", instr.Op))
		}
	case *ssa.UnOp:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			fr.setReg(ir, unop(instr, fr.reg(ix)))
		}
	case *ssa.ChangeInterface:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			fr.setReg(ir, fr.reg(ix))
		}
	case *ssa.ChangeType:
		typ := interp.preToType(instr.Type())
		ir := pfn.regIndex(instr)
		ix, kx, vx := pfn.regIndex3(instr.X)
		if kx.isStatic() {
			if vx == nil {
				vx = reflect.New(typ).Elem().Interface()
			}
			return func(fr *frame) {
				fr.setReg(ir, vx)
			}
		}
		return func(fr *frame) {
			x := fr.reg(ix)
			if x == nil {
				fr.setReg(ir, reflect.New(typ).Elem().Interface())
			} else {
				fr.setReg(ir, reflect.ValueOf(x).Convert(typ).Interface())
			}
		}
	case *ssa.Convert:
		return makeConvertInstr(pfn, interp, instr)
	case *ssa.MakeInterface:
		typ := interp.preToType(instr.Type())
		ir := pfn.regIndex(instr)
		ix, kx, vx := pfn.regIndex3(instr.X)
		if kx.isStatic() {
			if typ == tyEmptyInterface {
				return func(fr *frame) {
					fr.setReg(ir, vx)
				}
			}
			v := reflect.New(typ).Elem()
			if vx != nil {
				SetValue(v, reflect.ValueOf(vx))
			}
			vx = v.Interface()
			return func(fr *frame) {
				fr.setReg(ir, vx)
			}
		}
		if typ == tyEmptyInterface {
			return func(fr *frame) {
				fr.setReg(ir, fr.reg(ix))
			}
		}
		return func(fr *frame) {
			v := reflect.New(typ).Elem()
			if x := fr.reg(ix); x != nil {
				SetValue(v, reflect.ValueOf(x))
			}
			fr.setReg(ir, v.Interface())
		}
	case *ssa.MakeClosure:
		fn := instr.Fn.(*ssa.Function)
		typ := interp.preToType(fn.Type())
		ir := pfn.regIndex(instr)
		ib := make([]Register, len(instr.Bindings))
		for i, v := range instr.Bindings {
			ib[i] = pfn.regIndex(v)
		}
		pfn := interp.loadFunction(fn)
		return func(fr *frame) {
			var bindings []value
			for i, _ := range instr.Bindings {
				bindings = append(bindings, fr.reg(ib[i]))
			}
			v := interp.makeFunc(typ, pfn, bindings)
			fr.setReg(ir, v.Interface())
		}
	case *ssa.MakeChan:
		typ := interp.preToType(instr.Type())
		ir := pfn.regIndex(instr)
		is := pfn.regIndex(instr.Size)
		return func(fr *frame) {
			size := fr.reg(is)
			buffer := asInt(size)
			if buffer < 0 {
				panic(runtimeError("makechan: size out of range"))
			}
			fr.setReg(ir, reflect.MakeChan(typ, buffer).Interface())
		}
	case *ssa.MakeMap:
		typ := instr.Type()
		rtyp := interp.preToType(typ)
		key := typ.Underlying().(*types.Map).Key()
		if st, ok := key.Underlying().(*types.Struct); ok && hasUnderscore(st) {
			pfn.mapUnderscoreKey[typ] = true
		}
		ir := pfn.regIndex(instr)
		if instr.Reserve == nil {
			return func(fr *frame) {
				fr.setReg(ir, reflect.MakeMap(rtyp).Interface())
			}
		} else {
			iv := pfn.regIndex(instr.Reserve)
			return func(fr *frame) {
				reserve := asInt(fr.reg(iv))
				fr.setReg(ir, reflect.MakeMapWithSize(rtyp, reserve).Interface())
			}
		}
	case *ssa.MakeSlice:
		typ := interp.preToType(instr.Type())
		ir := pfn.regIndex(instr)
		il := pfn.regIndex(instr.Len)
		ic := pfn.regIndex(instr.Cap)
		return func(fr *frame) {
			Len := asInt(fr.reg(il))
			if Len < 0 || Len >= maxMemLen {
				panic(runtimeError("makeslice: len out of range"))
			}
			Cap := asInt(fr.reg(ic))
			if Cap < 0 || Cap >= maxMemLen {
				panic(runtimeError("makeslice: cap out of range"))
			}
			fr.setReg(ir, reflect.MakeSlice(typ, Len, Cap).Interface())
		}
	case *ssa.Slice:
		typ := interp.preToType(instr.Type())
		isNamed := typ.Kind() == reflect.Slice && typ != reflect.SliceOf(typ.Elem())
		_, makesliceCheck := instr.X.(*ssa.Alloc)
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		ih := pfn.regIndex(instr.High)
		il := pfn.regIndex(instr.Low)
		im := pfn.regIndex(instr.Max)
		if isNamed {
			return func(fr *frame) {
				fr.setReg(ir, slice(fr, instr, makesliceCheck, ix, ih, il, im).Convert(typ).Interface())
			}
		} else {
			return func(fr *frame) {
				fr.setReg(ir, slice(fr, instr, makesliceCheck, ix, ih, il, im).Interface())
			}
		}
	case *ssa.FieldAddr:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			v, err := FieldAddr(fr.reg(ix), instr.Field)
			if err != nil {
				panic(runtimeError(err.Error()))
			}
			fr.setReg(ir, v)
		}
	case *ssa.Field:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			v, err := Field(fr.reg(ix), instr.Field)
			if err != nil {
				panic(runtimeError(err.Error()))
			}
			fr.setReg(ir, v)
		}
	case *ssa.IndexAddr:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		ii := pfn.regIndex(instr.Index)
		return func(fr *frame) {
			x := fr.reg(ix)
			idx := fr.reg(ii)
			v := reflect.ValueOf(x)
			if v.Kind() == reflect.Ptr {
				v = v.Elem()
			}
			switch v.Kind() {
			case reflect.Slice:
			case reflect.Array:
			case reflect.Invalid:
				panic(runtimeError("invalid memory address or nil pointer dereference"))
			default:
				panic(fmt.Sprintf("unexpected x type in IndexAddr: %T", x))
			}
			index := asInt(idx)
			if index < 0 {
				panic(runtimeError(fmt.Sprintf("index out of range [%v]", index)))
			} else if length := v.Len(); index >= length {
				panic(runtimeError(fmt.Sprintf("index out of range [%v] with length %v", index, length)))
			}
			fr.setReg(ir, v.Index(index).Addr().Interface())
		}
	case *ssa.Index:
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		ii := pfn.regIndex(instr.Index)
		return func(fr *frame) {
			x := fr.reg(ix)
			idx := fr.reg(ii)
			v := reflect.ValueOf(x)
			fr.setReg(ir, v.Index(asInt(idx)).Interface())
		}
	case *ssa.Lookup:
		typ := interp.preToType(instr.X.Type())
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		ii := pfn.regIndex(instr.Index)
		switch typ.Kind() {
		case reflect.String:
			return func(fr *frame) {
				v := fr.reg(ix)
				idx := fr.reg(ii)
				fr.setReg(ir, reflect.ValueOf(v).String()[asInt(idx)])
			}
		case reflect.Map:
			if pfn.mapUnderscoreKey[instr.X.Type()] {
				return func(fr *frame) {
					m := fr.reg(ix)
					idx := fr.reg(ii)
					vm := reflect.ValueOf(m)
					vk := reflect.ValueOf(idx)
					for _, vv := range vm.MapKeys() {
						if equalStruct(vk, vv) {
							vk = vv
							break
						}
					}
					v := vm.MapIndex(vk)
					ok := v.IsValid()
					var rv value
					if ok {
						rv = v.Interface()
					} else {
						rv = reflect.New(typ.Elem()).Elem().Interface()
					}
					if instr.CommaOk {
						fr.setReg(ir, tuple{rv, ok})
					} else {
						fr.setReg(ir, rv)
					}
				}
			}
			return func(fr *frame) {
				m := fr.reg(ix)
				idx := fr.reg(ii)
				vm := reflect.ValueOf(m)
				v := vm.MapIndex(reflect.ValueOf(idx))
				ok := v.IsValid()
				var rv value
				if ok {
					rv = v.Interface()
				} else {
					rv = reflect.New(typ.Elem()).Elem().Interface()
				}
				if instr.CommaOk {
					fr.setReg(ir, tuple{rv, ok})
				} else {
					fr.setReg(ir, rv)
				}
			}
		default:
			panic("unreachable")
		}
	case *ssa.Select:
		ir := pfn.regIndex(instr)
		ic := make([]Register, len(instr.States))
		is := make([]Register, len(instr.States))
		for i, state := range instr.States {
			ic[i] = pfn.regIndex(state.Chan)
			if state.Send != nil {
				is[i] = pfn.regIndex(state.Send)
			}
		}
		return func(fr *frame) {
			var cases []reflect.SelectCase
			if !instr.Blocking {
				cases = append(cases, reflect.SelectCase{
					Dir: reflect.SelectDefault,
				})
			}
			for i, state := range instr.States {
				var dir reflect.SelectDir
				if state.Dir == types.RecvOnly {
					dir = reflect.SelectRecv
				} else {
					dir = reflect.SelectSend
				}
				ch := reflect.ValueOf(fr.reg(ic[i]))
				var send reflect.Value
				if state.Send != nil {
					v := fr.reg(is[i])
					if v == nil {
						send = reflect.New(ch.Type().Elem()).Elem()
					} else {
						send = reflect.ValueOf(v)
					}
				}
				cases = append(cases, reflect.SelectCase{
					Dir:  dir,
					Chan: ch,
					Send: send,
				})
			}
			chosen, recv, recvOk := reflect.Select(cases)
			if !instr.Blocking {
				chosen-- // default case should have index -1.
			}
			r := tuple{chosen, recvOk}
			for n, st := range instr.States {
				if st.Dir == types.RecvOnly {
					var v value
					if n == chosen && recvOk {
						// No need to copy since send makes an unaliased copy.
						v = recv.Interface()
					} else {
						typ := interp.toType(st.Chan.Type())
						v = reflect.New(typ.Elem()).Elem().Interface()
					}
					r = append(r, v)
				}
			}
			fr.setReg(ir, r)
		}
	case *ssa.SliceToArrayPointer:
		typ := interp.preToType(instr.Type())
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			x := fr.reg(ix)
			v := reflect.ValueOf(x)
			vLen := v.Len()
			tLen := typ.Elem().Len()
			if tLen > vLen {
				panic(runtimeError(fmt.Sprintf("cannot convert slice with length %v to pointer to array with length %v", vLen, tLen)))
			}
			fr.setReg(ir, v.Convert(typ).Interface())
		}
	case *ssa.Range:
		typ := interp.preToType(instr.X.Type())
		ir := pfn.regIndex(instr)
		ix := pfn.regIndex(instr.X)
		switch typ.Kind() {
		case reflect.String:
			return func(fr *frame) {
				v := fr.reg(ix)
				fr.setReg(ir, &stringIter{Reader: strings.NewReader(reflect.ValueOf(v).String())})
			}
		case reflect.Map:
			return func(fr *frame) {
				v := fr.reg(ix)
				fr.setReg(ir, &mapIter{iter: reflect.ValueOf(v).MapRange()})
			}
		default:
			panic("unreachable")
		}
	case *ssa.Next:
		ir := pfn.regIndex(instr)
		ii := pfn.regIndex(instr.Iter)
		if instr.IsString {
			return func(fr *frame) {
				fr.setReg(ir, fr.reg(ii).(*stringIter).next())
			}
		} else {
			return func(fr *frame) {
				fr.setReg(ir, fr.reg(ii).(*mapIter).next())
			}
		}
	case *ssa.TypeAssert:
		typ := interp.preToType(instr.AssertedType)
		ir := pfn.regIndex(instr)
		ix, kx, vx := pfn.regIndex3(instr.X)
		if kx.isStatic() {
			return func(fr *frame) {
				fr.setReg(ir, typeAssert(interp, instr, typ, vx))
			}
		}
		return func(fr *frame) {
			v := fr.reg(ix)
			fr.setReg(ir, typeAssert(interp, instr, typ, v))
		}
	case *ssa.Extract:
		if *instr.Referrers() == nil {
			return nil
		}
		ir := pfn.regIndex(instr)
		it := pfn.regIndex(instr.Tuple)
		return func(fr *frame) {
			fr.setReg(ir, fr.reg(it).(tuple)[instr.Index])
		}
	// Instructions executed for effect
	case *ssa.Jump:
		return func(fr *frame) {
			fr.pred, fr.block = fr.block.Index, fr.block.Succs[0]
			fr.pc = fr.pfn.Blocks[fr.block.Index]
		}
	case *ssa.If:
		ic := pfn.regIndex(instr.Cond)
		switch instr.Cond.Type().(type) {
		case *types.Basic:
			return func(fr *frame) {
				fr.pred = fr.block.Index
				if fr.reg(ic).(bool) {
					fr.block = fr.block.Succs[0]
				} else {
					fr.block = fr.block.Succs[1]
				}
				fr.pc = fr.pfn.Blocks[fr.block.Index]
			}
		default:
			return func(fr *frame) {
				fr.pred = fr.block.Index
				if v := fr.reg(ic); reflect.ValueOf(v).Bool() {
					fr.block = fr.block.Succs[0]
				} else {
					fr.block = fr.block.Succs[1]
				}
				fr.pc = fr.pfn.Blocks[fr.block.Index]
			}
		}
	case *ssa.Return:
		switch n := len(instr.Results); n {
		case 0:
			return func(fr *frame) {
				fr.pc = -1
			}
		case 1:
			ir := pfn.regIndex(instr.Results[0])
			return func(fr *frame) {
				fr.results = []Register{ir}
				fr.pc = -1
			}
		default:
			ir := make([]Register, n, n)
			for i, v := range instr.Results {
				ir[i] = pfn.regIndex(v)
			}
			return func(fr *frame) {
				fr.results = ir
				fr.pc = -1
			}
		}
	case *ssa.RunDefers:
		return func(fr *frame) {
			fr.runDefers()
		}
	case *ssa.Panic:
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			panic(targetPanic{fr.reg(ix)})
		}
	case *ssa.Go:
		iv, ia, ib := getCallIndex(pfn, &instr.Call)
		return func(fr *frame) {
			fn, args := interp.prepareCall(fr, &instr.Call, iv, ia, ib)
			atomic.AddInt32(&interp.goroutines, 1)
			go func() {
				interp.callDiscardsResult(nil, fn, args, instr.Call.Args)
				atomic.AddInt32(&interp.goroutines, -1)
			}()
		}
	case *ssa.Defer:
		iv, ia, ib := getCallIndex(pfn, &instr.Call)
		return func(fr *frame) {
			fn, args := interp.prepareCall(fr, &instr.Call, iv, ia, ib)
			fr.defers = &deferred{
				fn:      fn,
				args:    args,
				ssaArgs: instr.Call.Args,
				instr:   instr,
				tail:    fr.defers,
			}
		}
	case *ssa.Send:
		ic := pfn.regIndex(instr.Chan)
		ix := pfn.regIndex(instr.X)
		return func(fr *frame) {
			c := fr.reg(ic)
			x := fr.reg(ix)
			ch := reflect.ValueOf(c)
			if x == nil {
				ch.Send(reflect.New(ch.Type().Elem()).Elem())
			} else {
				ch.Send(reflect.ValueOf(x))
			}
		}
	case *ssa.Store:
		// skip struct field _
		if addr, ok := instr.Addr.(*ssa.FieldAddr); ok {
			if s, ok := addr.X.Type().(*types.Pointer).Elem().(*types.Struct); ok {
				if s.Field(addr.Field).Name() == "_" {
					return nil
				}
			}
		}
		ia := pfn.regIndex(instr.Addr)
		iv, kv, vv := pfn.regIndex3(instr.Val)
		if kv.isStatic() {
			if vv == nil {
				return func(fr *frame) {
					x := reflect.ValueOf(fr.reg(ia))
					SetValue(x.Elem(), reflect.New(x.Elem().Type()).Elem())
				}
			}
			return func(fr *frame) {
				x := reflect.ValueOf(fr.reg(ia))
				SetValue(x.Elem(), reflect.ValueOf(vv))
			}
		}
		return func(fr *frame) {
			x := reflect.ValueOf(fr.reg(ia))
			val := fr.reg(iv)
			v := reflect.ValueOf(val)
			if v.IsValid() {
				SetValue(x.Elem(), v)
			} else {
				SetValue(x.Elem(), reflect.New(x.Elem().Type()).Elem())
			}
		}
	case *ssa.MapUpdate:
		im := pfn.regIndex(instr.Map)
		ik := pfn.regIndex(instr.Key)
		iv, kv, vv := pfn.regIndex3(instr.Value)
		if pfn.mapUnderscoreKey[instr.Map.Type()] {
			if kv.isStatic() {
				return func(fr *frame) {
					vm := reflect.ValueOf(fr.reg(im))
					vk := reflect.ValueOf(fr.reg(ik))
					for _, v := range vm.MapKeys() {
						if equalStruct(vk, v) {
							vk = v
							break
						}
					}
					vm.SetMapIndex(vk, reflect.ValueOf(vv))
				}
			}
			return func(fr *frame) {
				vm := reflect.ValueOf(fr.reg(im))
				vk := reflect.ValueOf(fr.reg(ik))
				v := fr.reg(iv)
				for _, vv := range vm.MapKeys() {
					if equalStruct(vk, vv) {
						vk = vv
						break
					}
				}
				vm.SetMapIndex(vk, reflect.ValueOf(v))
			}
		}
		if kv.isStatic() {
			return func(fr *frame) {
				vm := reflect.ValueOf(fr.reg(im))
				vk := reflect.ValueOf(fr.reg(ik))
				vm.SetMapIndex(vk, reflect.ValueOf(vv))
			}
		} else {
			return func(fr *frame) {
				vm := reflect.ValueOf(fr.reg(im))
				vk := reflect.ValueOf(fr.reg(ik))
				v := fr.reg(iv)
				vm.SetMapIndex(vk, reflect.ValueOf(v))
			}
		}
	case *ssa.DebugRef:
		if v, ok := instr.Object().(*types.Var); ok {
			ix := pfn.regIndex(instr.X)
			return func(fr *frame) {
				ref := &DebugInfo{DebugRef: instr, fset: interp.fset}
				ref.toValue = func() (*types.Var, interface{}, bool) {
					return v, fr.reg(ix), true
				}
				interp.ctx.debugFunc(ref)
			}
		}
		return func(fr *frame) {
			ref := &DebugInfo{DebugRef: instr, fset: interp.fset}
			ref.toValue = func() (*types.Var, interface{}, bool) {
				return nil, nil, false
			}
			interp.ctx.debugFunc(ref)
		}
	default:
		panic(fmt.Errorf("unreachable %T", instr))
	}
}

func getCallIndex(pfn *Function, call *ssa.CallCommon) (iv Register, ia []Register, ib []Register) {
	iv = pfn.regIndex(call.Value)
	ia = make([]Register, len(call.Args), len(call.Args))
	for i, v := range call.Args {
		ia[i] = pfn.regIndex(v)
	}
	if f, ok := call.Value.(*ssa.MakeClosure); ok {
		ib = make([]Register, len(f.Bindings), len(f.Bindings))
		for i, binding := range f.Bindings {
			ib[i] = pfn.regIndex(binding)
		}
	}
	return
}

func makeConvertInstr(pfn *Function, interp *Interp, instr *ssa.Convert) func(fr *frame) {
	typ := interp.preToType(instr.Type())
	xtyp := interp.preToType(instr.X.Type())
	vk := xtyp.Kind()
	ir := pfn.regIndex(instr)
	ix := pfn.regIndex(instr.X)
	switch typ.Kind() {
	case reflect.UnsafePointer:
		if vk == reflect.Uintptr {
			return func(fr *frame) {
				v := reflect.ValueOf(fr.reg(ix))
				fr.setReg(ir, toUnsafePointer(v))
			}
		} else if vk == reflect.Ptr {
			return func(fr *frame) {
				v := reflect.ValueOf(fr.reg(ix))
				fr.setReg(ir, unsafe.Pointer(v.Pointer()))
			}
		}
	case reflect.Uintptr:
		if vk == reflect.UnsafePointer {
			return func(fr *frame) {
				v := reflect.ValueOf(fr.reg(ix))
				fr.setReg(ir, v.Pointer())
			}
		}
	case reflect.Ptr:
		if vk == reflect.UnsafePointer {
			return func(fr *frame) {
				v := reflect.ValueOf(fr.reg(ix))
				fr.setReg(ir, reflect.NewAt(typ.Elem(), unsafe.Pointer(v.Pointer())).Interface())
			}
		}
	case reflect.Slice:
		if vk == reflect.String {
			elem := typ.Elem()
			switch elem.Kind() {
			case reflect.Uint8:
				if elem.PkgPath() != "" {
					return func(fr *frame) {
						v := reflect.ValueOf(fr.reg(ix))
						dst := reflect.New(typ).Elem()
						dst.SetBytes([]byte(v.String()))
						fr.setReg(ir, dst.Interface())
					}
				}
			case reflect.Int32:
				if elem.PkgPath() != "" {
					return func(fr *frame) {
						v := reflect.ValueOf(fr.reg(ix))
						dst := reflect.New(typ).Elem()
						*(*[]rune)((*reflectValue)(unsafe.Pointer(&dst)).ptr) = []rune(v.String())
						fr.setReg(ir, dst.Interface())
					}
				}
			}
		}
	case reflect.String:
		if vk == reflect.Slice {
			elem := xtyp.Elem()
			switch elem.Kind() {
			case reflect.Uint8:
				if elem.PkgPath() != "" {
					return func(fr *frame) {
						v := reflect.ValueOf(fr.reg(ix))
						v = reflect.ValueOf(string(v.Bytes()))
						fr.setReg(ir, v.Convert(typ).Interface())
					}
				}
			case reflect.Int32:
				if elem.PkgPath() != "" {
					return func(fr *frame) {
						v := reflect.ValueOf(fr.reg(ix))
						v = reflect.ValueOf(*(*[]rune)(((*reflectValue)(unsafe.Pointer(&v))).ptr))
						fr.setReg(ir, v.Convert(typ).Interface())
					}
				}
			}
		}
	}
	return func(fr *frame) {
		v := reflect.ValueOf(fr.reg(ix))
		fr.setReg(ir, v.Convert(typ).Interface())
	}
}

func makeCallInstr(pfn *Function, interp *Interp, instr ssa.Value, call *ssa.CallCommon) func(fr *frame) {
	ir := pfn.regIndex(instr)
	iv, ia, ib := getCallIndex(pfn, call)
	switch fn := call.Value.(type) {
	case *ssa.Builtin:
		fname := fn.Name()
		return func(fr *frame) {
			interp.callBuiltinByStack(fr, fname, call.Args, ir, ia)
		}
	case *ssa.MakeClosure:
		ifn := interp.loadFunction(fn.Fn.(*ssa.Function))
		ia = append(ia, ib...)
		if ifn.Recover == nil {
			return func(fr *frame) {
				interp.callFunctionByStackNoRecover(fr, ifn, ir, ia)
			}
		}
		return func(fr *frame) {
			interp.callFunctionByStack(fr, ifn, ir, ia)
		}
	case *ssa.Function:
		// "static func/method call"
		if fn.Blocks == nil {
			ext, ok := findExternFunc(interp, fn)
			if !ok {
				// skip pkg.init
				if fn.Pkg != nil && fn.Name() == "init" {
					return nil
				}
				panic(fmt.Errorf("no code for function: %v", fn))
			}
			return func(fr *frame) {
				interp.callExternalByStack(fr, ext, ir, ia)
			}
		}
		ifn := interp.loadFunction(fn)
		if ifn.Recover == nil {
			return func(fr *frame) {
				interp.callFunctionByStackNoRecover(fr, ifn, ir, ia)
			}
		}
		return func(fr *frame) {
			interp.callFunctionByStack(fr, ifn, ir, ia)
		}
	}
	// "dynamic method call" // ("invoke" mode)
	if call.IsInvoke() {
		return makeCallMethodInstr(interp, instr, call, ir, iv, ia)
	}
	// dynamic func call
	typ := interp.preToType(call.Value.Type())
	if typ.Kind() != reflect.Func {
		panic("unsupport")
	}
	return func(fr *frame) {
		fn := fr.reg(iv)
		if fv, n := funcval.Get(fn); n == 1 {
			c := (*makeFuncVal)(unsafe.Pointer(fv))
			if c.pfn.Recover == nil {
				interp.callFunctionByStackNoRecoverWithEnv(fr, c.pfn, ir, ia, c.env)
			} else {
				interp.callFunctionByStackWithEnv(fr, c.pfn, ir, ia, c.env)
			}
		} else {
			v := reflect.ValueOf(fn)
			interp.callExternalByStack(fr, v, ir, ia)
		}
	}
}

// makeFuncVal sync with Interp.makeFunc
// func (i *Interp) makeFunc(typ reflect.Type, pfn *Function, env []value) reflect.Value {
// 	return reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
// 		return i.callFunctionByReflect(i.tryDeferFrame(), typ, pfn, args, env)
// 	})
// }
type makeFuncVal struct {
	funcval.FuncVal
	interp *Interp
	typ    reflect.Type
	pfn    *Function
	env    []interface{}
}

var (
	typeOfType      = reflect.TypeOf(reflect.TypeOf(0))
	vfnMethod       = reflect.ValueOf(reflectx.MethodByIndex)
	vfnMethodByName = reflect.ValueOf(reflectx.MethodByName)
)

func findUserMethod(typ reflect.Type, name string) (ext reflect.Value, ok bool) {
	if m, ok := reflectx.MethodByName(typ, name); ok {
		return m.Func, true
	}
	return
}

func findExternMethod(typ reflect.Type, name string) (ext reflect.Value, ok bool) {
	if typ == typeOfType {
		switch name {
		case "Method":
			return vfnMethod, true
		case "MethodByName":
			return vfnMethodByName, true
		}
	}
	if m, ok := typ.MethodByName(name); ok {
		return m.Func, true
	}
	return
}

func (i *Interp) findMethod(typ reflect.Type, mname string) (fn *ssa.Function, ok bool) {
	if mset, mok := i.msets[typ]; mok {
		fn, ok = mset[mname]
	}
	return
}

func makeCallMethodInstr(interp *Interp, instr ssa.Value, call *ssa.CallCommon, ir Register, iv Register, ia []Register) func(fr *frame) {
	mname := call.Method.Name()
	ia = append([]Register{iv}, ia...)
	var found bool
	var ext reflect.Value
	return func(fr *frame) {
		v := fr.reg(iv)
		rtype := reflect.TypeOf(v)
		// find user type method *ssa.Function
		if mset, ok := interp.msets[rtype]; ok {
			if fn, ok := mset[mname]; ok {
				interp.callFunctionByStack(fr, interp.funcs[fn], ir, ia)
				return
			}
			ext, found = findUserMethod(rtype, mname)
		} else {
			ext, found = findExternMethod(rtype, mname)
		}
		if !found {
			panic(fmt.Errorf("no code for method: %v.%v", rtype, mname))
		}
		interp.callExternalByStack(fr, ext, ir, ia)
	}
}
