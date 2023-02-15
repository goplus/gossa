/*
 * Copyright (c) 2022 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package igop

import (
	"go/constant"
	"reflect"
	"sort"
)

var (
	registerPkgs = make(map[string]*Package)
)

// PackageList return all register packages
func PackageList() (list []string) {
	for pkg := range registerPkgs {
		list = append(list, pkg)
	}
	sort.Strings(list)
	return
}

// LookupPackage lookup register pkgs
func LookupPackage(name string) (pkg *Package, ok bool) {
	pkg, ok = registerPkgs[name]
	return
}

// RegisterPackage register pkg
func RegisterPackage(pkg *Package) {
	if p, ok := registerPkgs[pkg.Path]; ok {
		p.merge(pkg)
		return
	}
	registerPkgs[pkg.Path] = pkg
	//	externPackages[pkg.Path] = true
}

type TypedConst struct {
	Typ   reflect.Type
	Value constant.Value
}

type UntypedConst struct {
	Typ   string
	Value constant.Value
}

type Package struct {
	Interfaces    map[string]reflect.Type
	NamedTypes    map[string]reflect.Type
	AliasTypes    map[string]reflect.Type
	Vars          map[string]reflect.Value
	Funcs         map[string]reflect.Value
	TypedConsts   map[string]TypedConst
	UntypedConsts map[string]UntypedConst
	Deps          map[string]string // path -> name
	Name          string
	Path          string
}

// merge same package
func (p *Package) merge(same *Package) {
	for k, v := range same.Interfaces {
		p.Interfaces[k] = v
	}
	for k, v := range same.NamedTypes {
		p.NamedTypes[k] = v
	}
	for k, v := range same.Vars {
		p.Vars[k] = v
	}
	for k, v := range same.Funcs {
		p.Funcs[k] = v
	}
	for k, v := range same.UntypedConsts {
		p.UntypedConsts[k] = v
	}
}

var (
	externValues = make(map[string]reflect.Value)
)

// RegisterExternal is register external function for no function body
func RegisterExternal(key string, i interface{}) {
	externValues[key] = reflect.ValueOf(i)
}
