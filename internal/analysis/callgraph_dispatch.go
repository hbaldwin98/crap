package analysis

import (
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type callGraphImplementer struct {
	packages   []*packages.Package
	moduleRoot string
	namedTypes []*types.Named
	interfaces map[*types.Interface]map[string][]*types.Func
}

func newCallGraphImplementer(pkgs []*packages.Package, moduleRoot string) *callGraphImplementer {
	return &callGraphImplementer{
		packages: pkgs, moduleRoot: moduleRoot,
		namedTypes: inModuleNamedTypes(pkgs, moduleRoot),
		interfaces: make(map[*types.Interface]map[string][]*types.Func),
	}
}

func inModuleNamedTypes(pkgs []*packages.Package, moduleRoot string) []*types.Named {
	seen := make(map[string]struct{})
	named := make([]*types.Named, 0)
	for _, pkg := range pkgs {
		if _, ok := seen[pkg.PkgPath]; ok {
			continue
		}
		seen[pkg.PkgPath] = struct{}{}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			typeName, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || typeName.IsAlias() {
				continue
			}
			definition, ok := typeName.Type().(*types.Named)
			if !ok {
				continue
			}
			if definition.TypeParams().Len() > 0 {
				continue
			}
			if !inModuleDirectory(moduleRoot, definition.Obj().Pos(), pkg.Fset) {
				continue
			}
			named = append(named, definition)
		}
	}
	sort.Slice(named, func(i, j int) bool {
		if named[i].Obj().Pkg().Path() != named[j].Obj().Pkg().Path() {
			return named[i].Obj().Pkg().Path() < named[j].Obj().Pkg().Path()
		}
		return named[i].Obj().Name() < named[j].Obj().Name()
	})
	return named
}

func inModuleDirectory(moduleRoot string, position token.Pos, fset *token.FileSet) bool {
	resolved := fset.Position(position)
	relative, err := filepath.Rel(moduleRoot, resolved.Filename)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".."
}

func (implementer *callGraphImplementer) implementations(interfaceType *types.Interface, method *types.Func) []*types.Func {
	methods, cached := implementer.interfaces[interfaceType]
	if !cached {
		methods = implementer.interfaceImplementations(interfaceType)
		implementer.interfaces[interfaceType] = methods
	}
	return methods[method.Name()]
}

func (implementer *callGraphImplementer) interfaceImplementations(interfaceType *types.Interface) map[string][]*types.Func {
	methods := make(map[string][]*types.Func, interfaceType.NumMethods())
	for index := 0; index < interfaceType.NumMethods(); index++ {
		interfaceMethod := interfaceType.Method(index)
		for _, candidate := range implementer.namedTypes {
			implementation := namedTypeMethod(candidate, interfaceMethod, interfaceType)
			if implementation == nil {
				continue
			}
			methods[interfaceMethod.Name()] = append(methods[interfaceMethod.Name()], implementation)
		}
	}
	return methods
}

func namedTypeMethod(candidate *types.Named, interfaceMethod *types.Func, interfaceType *types.Interface) *types.Func {
	pointer := types.NewPointer(candidate)
	if !types.Implements(candidate, interfaceType) && !types.Implements(pointer, interfaceType) {
		return nil
	}
	lookup, _, _ := types.LookupFieldOrMethod(candidate, true, interfaceMethod.Pkg(), interfaceMethod.Name())
	if implementation, ok := lookup.(*types.Func); ok {
		return implementation
	}
	lookup, _, _ = types.LookupFieldOrMethod(pointer, true, interfaceMethod.Pkg(), interfaceMethod.Name())
	if implementation, ok := lookup.(*types.Func); ok {
		return implementation
	}
	return nil
}
