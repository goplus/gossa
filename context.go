package igop

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goplus/igop/testmain"
	"github.com/visualfc/gomod"
	"golang.org/x/tools/go/ssa"
)

// Mode is a bitmask of options affecting the interpreter.
type Mode uint

const (
	DisableRecover       Mode = 1 << iota // Disable recover() in target programs; show interpreter crash instead.
	DisableCustomBuiltin                  // Disable load custom builtin func
	EnableDumpImports                     // print typesimports
	EnableDumpInstr                       // Print packages & SSA instruction code
	EnableTracing                         // Print a trace of all instructions as they are interpreted.
	EnablePrintAny                        // Enable builtin print for any type ( struct/array )
)

// Loader types loader interface
type Loader interface {
	Import(path string) (*types.Package, error)
	Installed(path string) (*Package, bool)
	Packages() []*types.Package
	LookupReflect(typ types.Type) (reflect.Type, bool)
	LookupTypes(typ reflect.Type) (types.Type, bool)
	SetImport(path string, pkg *types.Package, load func() error) error
}

// Context ssa context
type Context struct {
	Loader       Loader                                           // types loader
	FileSet      *token.FileSet                                   // file set
	Mode         Mode                                             // mode
	ParserMode   parser.Mode                                      // parser mode
	BuilderMode  ssa.BuilderMode                                  // ssa builder mode
	BuildContext build.Context                                    // build context
	Lookup       func(root, path string) (dir string, found bool) // lookup external import
	pkgs         map[string]*sourcePackage                        // imports
	override     map[string]reflect.Value                         // override function
	output       io.Writer                                        // capture print/println output
	callForPool  int                                              // least call count for enable function pool
	conf         *types.Config                                    // types check config
	evalMode     bool                                             // eval mode
	evalInit     map[string]bool                                  // eval init check
	evalCallFn   func(interp *Interp, call *ssa.Call, res ...interface{})
	debugFunc    func(*DebugInfo) // debug func
	root         string           // project root
	mod          *gomod.Package   // lookup path for go.mod
}

func (ctx *Context) setRoot(root string) {
	ctx.root = root
	ctx.mod = nil
}

func (ctx *Context) lookupPath(path string) (dir string, found bool) {
	if ctx.Lookup != nil {
		dir, found = ctx.Lookup(ctx.root, path)
		if found {
			return
		}
	}
	if ctx.mod == nil {
		var err error
		ctx.mod, err = gomod.Load(ctx.root, &ctx.BuildContext)
		if err != nil {
			panic(err)
		}
	}
	_, dir, found = ctx.mod.Lookup(path)
	if !found {
		bp, err := build.Import(path, ctx.root, build.FindOnly)
		if err == nil && bp.ImportPath == path {
			return bp.Dir, true
		}
	}
	return
}

type sourcePackage struct {
	Context *Context
	Package *types.Package
	Info    *types.Info
	Files   []*ast.File
	Dir     string
}

func (sp *sourcePackage) Load() (err error) {
	if sp.Info == nil {
		sp.Info = &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		}
		if err := types.NewChecker(sp.Context.conf, sp.Context.FileSet, sp.Package, sp.Info).Files(sp.Files); err != nil {
			return err
		}
	}
	return
}

// NewContext create a new Context
func NewContext(mode Mode) *Context {
	ctx := &Context{
		Loader:       NewTypesLoader(mode),
		FileSet:      token.NewFileSet(),
		Mode:         mode,
		ParserMode:   parser.AllErrors,
		BuilderMode:  0, //ssa.SanityCheckFunctions,
		BuildContext: build.Default,
		pkgs:         make(map[string]*sourcePackage),
		override:     make(map[string]reflect.Value),
		callForPool:  64,
	}
	if mode&EnableDumpInstr != 0 {
		ctx.BuilderMode |= ssa.PrintFunctions
	}
	ctx.conf = &types.Config{
		Importer: NewImporter(ctx),
	}
	return ctx
}

func (ctx *Context) IsEvalMode() bool {
	return ctx.evalMode
}

func (ctx *Context) SetEvalMode(b bool) {
	ctx.evalMode = b
	ctx.conf.DisableUnusedImportCheck = b
}

// SetLeastCallForEnablePool set least call count for enable function pool, default 64
func (ctx *Context) SetLeastCallForEnablePool(count int) {
	ctx.callForPool = count
}

func (ctx *Context) SetDebug(fn func(*DebugInfo)) {
	ctx.BuilderMode |= ssa.GlobalDebug
	ctx.debugFunc = fn
}

// SetOverrideFunction register external function to override function.
// match func fullname and signature
func (ctx *Context) SetOverrideFunction(key string, fn interface{}) {
	ctx.override[key] = reflect.ValueOf(fn)
}

// ClearOverrideFunction reset override function
func (ctx *Context) ClearOverrideFunction(key string) {
	delete(ctx.override, key)
}

// SetPrintOutput is captured builtin print/println output
func (ctx *Context) SetPrintOutput(output *bytes.Buffer) {
	ctx.output = output
}

func (ctx *Context) writeOutput(data []byte) (n int, err error) {
	if ctx.output != nil {
		return ctx.output.Write(data)
	}
	return os.Stdout.Write(data)
}

func importPathForDir(dir string) (string, error) {
	cmd := exec.Command("go", "list")
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (ctx *Context) LoadDir(dir string, test bool) (pkg *ssa.Package, err error) {
	var sp *sourcePackage
	if test {
		sp, err = ctx.loadTestPackage(dir)
	} else {
		sp, err = ctx.loadPackage("main", dir)
	}
	if err != nil {
		return nil, err
	}
	if ctx.Mode&DisableCustomBuiltin == 0 {
		if f, err := ParseBuiltin(ctx.FileSet, sp.Package.Name()); err == nil {
			sp.Files = append([]*ast.File{f}, sp.Files...)
		}
	}
	ctx.setRoot(dir)
	if dir != "." {
		if wd, err := os.Getwd(); err == nil {
			os.Chdir(dir)
			defer os.Chdir(wd)
		}
	}
	err = sp.Load()
	if err != nil {
		return nil, err
	}
	return ctx.buildPackage(sp)
}

func RegisterFileProcess(ext string, fn SourceProcessFunc) {
	sourceProcessor[ext] = fn
}

type SourceProcessFunc func(ctx *Context, filename string, src interface{}) ([]byte, error)

var (
	sourceProcessor = make(map[string]SourceProcessFunc)
)

func (ctx *Context) AddImportFile(path string, filename string, src interface{}) (err error) {
	_, err = ctx.addImportFile(path, filename, src)
	return
}

func (ctx *Context) AddImport(path string, dir string) (err error) {
	_, err = ctx.addImport(path, dir)
	return
}

func (ctx *Context) addImportFile(path string, filename string, src interface{}) (*sourcePackage, error) {
	tp, err := ctx.loadPackageFile(path, filename, src)
	if err != nil {
		return nil, err
	}
	ctx.Loader.SetImport(path, tp.Package, tp.Load)
	return tp, nil
}

func (ctx *Context) addImport(path string, dir string) (*sourcePackage, error) {
	tp, err := ctx.loadPackage(path, dir)
	if err != nil {
		return nil, err
	}
	ctx.Loader.SetImport(path, tp.Package, tp.Load)
	return tp, nil
}

func (ctx *Context) loadPackageFile(path string, filename string, src interface{}) (*sourcePackage, error) {
	file, err := ctx.ParseFile(filename, src)
	if err != nil {
		return nil, err
	}
	pkg := types.NewPackage(path, file.Name.Name)
	tp := &sourcePackage{
		Context: ctx,
		Package: pkg,
		Files:   []*ast.File{file},
	}
	ctx.pkgs[path] = tp
	return tp, nil
}

func (ctx *Context) loadPackage(path string, dir string) (*sourcePackage, error) {
	bp, err := ctx.BuildContext.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	files, err := ctx.parseGoFiles(dir, append(bp.GoFiles, bp.CgoFiles...))
	if err != nil {
		return nil, err
	}
	tp := &sourcePackage{
		Package: types.NewPackage(path, bp.Name),
		Files:   files,
		Dir:     dir,
		Context: ctx,
	}
	ctx.pkgs[path] = tp
	return tp, nil
}

func (ctx *Context) loadTestPackage(dir string) (*sourcePackage, error) {
	importPath, err := importPathForDir(dir)
	if err != nil {
		return nil, err
	}
	bp, err := ctx.BuildContext.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	if len(bp.TestGoFiles) == 0 && len(bp.XTestGoFiles) == 0 {
		return nil, ErrNoTestFiles
	}
	bp.ImportPath = importPath
	files, err := ctx.parseGoFiles(dir, append(append(bp.GoFiles, bp.CgoFiles...), bp.TestGoFiles...))
	if err != nil {
		return nil, err
	}
	tp := &sourcePackage{
		Package: types.NewPackage(importPath, bp.Name),
		Files:   files,
		Dir:     dir,
		Context: ctx,
	}
	ctx.pkgs[importPath] = tp
	if len(bp.XTestGoFiles) > 0 {
		files, err := ctx.parseGoFiles(dir, bp.XTestGoFiles)
		if err != nil {
			return nil, err
		}
		tp := &sourcePackage{
			Package: types.NewPackage(importPath+"_test", bp.Name+"_test"),
			Files:   files,
			Dir:     dir,
			Context: ctx,
		}
		ctx.pkgs[importPath+"_test"] = tp
	}
	data, err := testmain.Load(bp)
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(ctx.FileSet, "_testmain.go", data, 0)
	if err != nil {
		return nil, err
	}
	return &sourcePackage{
		Package: types.NewPackage(importPath+"$testmain", "main"),
		Files:   []*ast.File{f},
		Dir:     dir,
		Context: ctx,
	}, nil
}

func (ctx *Context) parseGoFiles(dir string, filenames []string) ([]*ast.File, error) {
	files := make([]*ast.File, len(filenames))
	errors := make([]error, len(filenames))

	var wg sync.WaitGroup
	wg.Add(len(filenames))
	for i, filename := range filenames {
		go func(i int, filepath string) {
			defer wg.Done()
			files[i], errors[i] = parser.ParseFile(ctx.FileSet, filepath, nil, 0)
		}(i, filepath.Join(dir, filename))
	}
	wg.Wait()

	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (ctx *Context) LoadFile(filename string, src interface{}) (*ssa.Package, error) {
	file, err := ctx.ParseFile(filename, src)
	if err != nil {
		return nil, err
	}
	root, _ := filepath.Split(filename)
	ctx.setRoot(root)
	return ctx.LoadAstFile("main", file)
}

func (ctx *Context) ParseFile(filename string, src interface{}) (*ast.File, error) {
	if ext := filepath.Ext(filename); ext != "" {
		if fn, ok := sourceProcessor[ext]; ok {
			data, err := fn(ctx, filename, src)
			if err != nil {
				return nil, err
			}
			src = data
		}
	}
	return parser.ParseFile(ctx.FileSet, filename, src, ctx.ParserMode)
}

func (ctx *Context) LoadAstFile(path string, file *ast.File) (*ssa.Package, error) {
	files := []*ast.File{file}
	if ctx.Mode&DisableCustomBuiltin == 0 {
		if f, err := ParseBuiltin(ctx.FileSet, file.Name.Name); err == nil {
			files = []*ast.File{f, file}
		}
	}
	sp := &sourcePackage{
		Context: ctx,
		Package: types.NewPackage(path, file.Name.Name),
		Files:   files,
	}
	err := sp.Load()
	if err != nil {
		return nil, err
	}
	return ctx.buildPackage(sp)
}

func (ctx *Context) LoadAstPackage(path string, apkg *ast.Package) (*ssa.Package, error) {
	var files []*ast.File
	for _, f := range apkg.Files {
		files = append(files, f)
	}
	if ctx.Mode&DisableCustomBuiltin == 0 {
		if f, err := ParseBuiltin(ctx.FileSet, apkg.Name); err == nil {
			files = append([]*ast.File{f}, files...)
		}
	}
	sp := &sourcePackage{
		Context: ctx,
		Package: types.NewPackage(path, apkg.Name),
		Files:   files,
	}
	err := sp.Load()
	if err != nil {
		return nil, err
	}
	return ctx.buildPackage(sp)
}

func (ctx *Context) RunPkg(mainPkg *ssa.Package, input string, args []string) (exitCode int, err error) {
	// reset os args and flag
	os.Args = []string{input}
	if args != nil {
		os.Args = append(os.Args, args...)
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	interp, err := ctx.NewInterp(mainPkg)
	if err != nil {
		return 2, err
	}
	if err = interp.RunInit(); err != nil {
		return 2, err
	}
	return interp.RunMain()
}

func (ctx *Context) RunFunc(mainPkg *ssa.Package, fnname string, args ...Value) (ret Value, err error) {
	interp, err := ctx.NewInterp(mainPkg)
	if err != nil {
		return nil, err
	}
	return interp.RunFunc(fnname, args...)
}

func (ctx *Context) NewInterp(mainPkg *ssa.Package) (*Interp, error) {
	return NewInterp(ctx, mainPkg)
}

func (ctx *Context) TestPkg(pkg *ssa.Package, input string, args []string) error {
	var failed bool
	start := time.Now()
	defer func() {
		sec := time.Since(start).Seconds()
		if failed {
			fmt.Fprintf(os.Stdout, "FAIL\t%s %0.3fs\n", input, sec)
		} else {
			fmt.Fprintf(os.Stdout, "ok\t%s %0.3fs\n", input, sec)
		}
	}()
	os.Args = []string{input}
	if args != nil {
		os.Args = append(os.Args, args...)
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	interp, err := NewInterp(ctx, pkg)
	if err != nil {
		failed = true
		fmt.Printf("create interp failed: %v\n", err)
	}
	if err = interp.RunInit(); err != nil {
		failed = true
		fmt.Printf("init error: %v\n", err)
	}
	exitCode, _ := interp.RunMain()
	if exitCode != 0 {
		failed = true
	}
	if failed {
		return ErrTestFailed
	}
	return nil
}

func (ctx *Context) RunFile(filename string, src interface{}, args []string) (exitCode int, err error) {
	pkg, err := ctx.LoadFile(filename, src)
	if err != nil {
		return 2, err
	}
	return ctx.RunPkg(pkg, filename, args)
}

func (ctx *Context) Run(path string, args []string) (exitCode int, err error) {
	if strings.HasSuffix(path, ".go") {
		return ctx.RunFile(path, nil, args)
	}
	pkg, err := ctx.LoadDir(path, false)
	if err != nil {
		return 2, err
	}
	if !isMainPkg(pkg) {
		return 2, ErrNotFoundMain
	}
	return ctx.RunPkg(pkg, path, args)
}

func isMainPkg(pkg *ssa.Package) bool {
	return pkg.Pkg.Name() == "main" && pkg.Func("main") != nil
}

func (ctx *Context) RunTest(dir string, args []string) error {
	pkg, err := ctx.LoadDir(dir, true)
	if err != nil {
		if err == ErrNoTestFiles {
			fmt.Println("?", err)
			return nil
		}
		return err
	}
	if filepath.IsAbs(dir) {
		os.Chdir(dir)
	}
	return ctx.TestPkg(pkg, dir, args)
}

func (ctx *Context) checkTypesInfo(pkg *types.Package, files []*ast.File) (*types.Info, error) {
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Scopes:     make(map[ast.Node]*types.Scope),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	if err := types.NewChecker(ctx.conf, ctx.FileSet, pkg, info).Files(files); err != nil {
		return nil, err
	}
	return info, nil
}

func (ctx *Context) buildPackage(sp *sourcePackage) (pkg *ssa.Package, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("build ssa package error: %v", e)
		}
	}()
	prog := ssa.NewProgram(ctx.FileSet, ctx.BuilderMode)
	// Create SSA packages for all imports.
	// Order is not significant.
	created := make(map[*types.Package]bool)
	var createAll func(pkgs []*types.Package)
	createAll = func(pkgs []*types.Package) {
		for _, p := range pkgs {
			if !created[p] {
				created[p] = true
				createAll(p.Imports())
				if pkg, ok := ctx.pkgs[p.Path()]; ok {
					if ctx.Mode&EnableDumpImports != 0 {
						if pkg.Dir != "" {
							fmt.Println("# imported", p.Path(), pkg.Dir)
						} else {
							fmt.Println("# imported", p.Path(), "source")
						}
					}
					prog.CreatePackage(p, pkg.Files, pkg.Info, true).Build()
				} else {
					var indirect bool
					if !p.Complete() {
						indirect = true
						p.MarkComplete()
					}
					if ctx.Mode&EnableDumpImports != 0 {
						if indirect {
							fmt.Println("# indirect", p.Path())
						} else {
							fmt.Println("# imported", p.Path())
						}
					}
					prog.CreatePackage(p, nil, nil, true).Build()
				}
			}
		}
	}
	createAll(sp.Package.Imports())
	// Create and build the primary package.
	pkg = prog.CreatePackage(sp.Package, sp.Files, sp.Info, false)
	pkg.Build()
	return
}

func RunFile(filename string, src interface{}, args []string, mode Mode) (exitCode int, err error) {
	ctx := NewContext(mode)
	return ctx.RunFile(filename, src, args)
}

func Run(path string, args []string, mode Mode) (exitCode int, err error) {
	ctx := NewContext(mode)
	return ctx.Run(path, args)
}

func RunTest(path string, args []string, mode Mode) error {
	ctx := NewContext(mode)
	return ctx.RunTest(path, args)
}

var (
	builtinPkg = &Package{
		Name:          "builtin",
		Path:          "github.com/goplus/igop/builtin",
		Deps:          make(map[string]string),
		Interfaces:    map[string]reflect.Type{},
		NamedTypes:    map[string]reflect.Type{},
		AliasTypes:    map[string]reflect.Type{},
		Vars:          map[string]reflect.Value{},
		Funcs:         map[string]reflect.Value{},
		TypedConsts:   map[string]TypedConst{},
		UntypedConsts: map[string]UntypedConst{},
	}
	builtinPrefix = "Builtin_"
)

func init() {
	RegisterPackage(builtinPkg)
}

func RegisterCustomBuiltin(key string, fn interface{}) error {
	v := reflect.ValueOf(fn)
	switch v.Kind() {
	case reflect.Func:
		if !strings.HasPrefix(key, builtinPrefix) {
			key = builtinPrefix + key
		}
		builtinPkg.Funcs[key] = v
		typ := v.Type()
		for i := 0; i < typ.NumIn(); i++ {
			checkBuiltinDeps(typ.In(i))
		}
		for i := 0; i < typ.NumOut(); i++ {
			checkBuiltinDeps(typ.Out(i))
		}
		return nil
	}
	return ErrNoFunction
}

func checkBuiltinDeps(typ reflect.Type) {
	if path := typ.PkgPath(); path != "" {
		v := strings.Split(path, "/")
		builtinPkg.Deps[path] = v[len(v)-1]
	}
}

func builtinFuncList() []string {
	var list []string
	for k, v := range builtinPkg.Funcs {
		if strings.HasPrefix(k, builtinPrefix) {
			name := k[len(builtinPrefix):]
			typ := v.Type()
			var ins []string
			var outs []string
			var call []string
			numIn := typ.NumIn()
			numOut := typ.NumOut()
			if typ.IsVariadic() {
				for i := 0; i < numIn-1; i++ {
					ins = append(ins, fmt.Sprintf("p%v %v", i, typ.In(i).String()))
					call = append(call, fmt.Sprintf("p%v", i))
				}
				ins = append(ins, fmt.Sprintf("p%v ...%v", numIn-1, typ.In(numIn-1).Elem().String()))
				call = append(call, fmt.Sprintf("p%v...", numIn-1))
			} else {
				for i := 0; i < numIn; i++ {
					ins = append(ins, fmt.Sprintf("p%v %v", i, typ.In(i).String()))
					call = append(call, fmt.Sprintf("p%v", i))
				}
			}
			for i := 0; i < numOut; i++ {
				outs = append(outs, typ.Out(i).String())
			}
			var str string
			if numOut > 0 {
				str = fmt.Sprintf("func %v(%v)(%v) { return builtin.%v(%v) }",
					name, strings.Join(ins, ","), strings.Join(outs, ","), k, strings.Join(call, ","))
			} else {
				str = fmt.Sprintf("func %v(%v) { builtin.%v(%v) }",
					name, strings.Join(ins, ","), k, strings.Join(call, ","))
			}
			list = append(list, str)
		}
	}
	return list
}

func ParseBuiltin(fset *token.FileSet, pkg string) (*ast.File, error) {
	list := builtinFuncList()
	if len(list) == 0 {
		return nil, os.ErrInvalid
	}
	var deps []string
	for k := range builtinPkg.Deps {
		deps = append(deps, strconv.Quote(k))
	}
	sort.Strings(deps)
	src := fmt.Sprintf(`package %v
import (
	"github.com/goplus/igop/builtin"
	%v
)
%v
`, pkg, strings.Join(deps, "\n"), strings.Join(list, "\n"))
	return parser.ParseFile(fset, "gossa_builtin.go", src, 0)
}
