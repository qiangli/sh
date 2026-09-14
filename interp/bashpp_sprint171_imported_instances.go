// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Instantiations of imported generic functions.
//
// The dependency helper registers an imported package's exported functions
// by name (`maps.Clone`), which has no meaning for a generic function: a
// generic function is not a value until it is instantiated, so the helper
// registered nothing and every call of one failed as an unknown symbol. The
// front end records each call's type arguments — spelled by the program or
// inferred by the checker — on the call, and the instantiation closure
// (bashpp_sprint165_runtime_instantiations.go) substitutes an enclosing
// generic body's bindings into them, so the concrete instantiations the
// program reaches are known before the helper is built. Each is registered
// under its instantiated spelling, and the call names that spelling.

// bashPPImportedInstance is one concrete instantiation of an imported
// generic function, rendered for the helper: Key is the symbol the call
// names, Selector the imported function (`alias.Name`) and Args the type
// arguments as Go source with the program's import aliases. refs names the
// original local types the arguments mention; host-only.
type bashPPImportedInstance struct {
	Key      string
	Selector string
	Args     []string
	refs     map[string]bool
}

// bashPPImportedInstanceKey is the symbol an instantiated imported generic
// function is registered under and called by: the selector followed by the
// instance suffix.
func bashPPImportedInstanceKey(selector string, args []syntax.BashPPTypeExpr) string {
	return selector + bashPPImportedInstanceSuffix(args)
}

// bashPPImportedInstanceSuffix is the interpreter's spelling of an
// instantiation's type arguments, `[T1, T2]`; a call carries it beside its
// selector (bashPPBridgeRequest.Instance) so the selector itself stays the
// name every host-side policy is keyed on.
func bashPPImportedInstanceSuffix(args []syntax.BashPPTypeExpr) string {
	return "[" + bashPPInstantiationArgsText(args) + "]"
}

// bashPPImportedInstances renders every reached instantiation of an imported
// generic function whose type arguments are expressible in the helper. An
// instantiation with an inexpressible argument is not registered, and its
// call fails as before.
func (r *Runner) bashPPImportedInstances() []bashPPImportedInstance {
	reached := r.bashPPReachedImportedInstantiations()
	if len(reached) == 0 {
		return nil
	}
	local := r.bashPPLocalTypeRenderer()
	keys := make([]string, 0, len(reached))
	for key := range reached {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []bashPPImportedInstance
	for _, key := range keys {
		item := reached[key]
		local.refs = map[string]bool{}
		args := make([]string, len(item.args))
		expressible := true
		for i, arg := range item.args {
			rendered, ok := local.source(arg, 0)
			if !ok {
				expressible = false
				break
			}
			args[i] = rendered
		}
		if !expressible {
			continue
		}
		out = append(out, bashPPImportedInstance{Key: key, Selector: item.alias + "." + item.name, Args: args, refs: local.refs})
	}
	return out
}

// bashPPLocalTypeRenderer is the local type set bashPPLocalTypeDescriptors
// renders declarations with, built from the same declarations under the
// same ambiguity rule, for rendering a type expression outside a declaration.
func (r *Runner) bashPPLocalTypeRenderer() *bashPPLocalTypeSet {
	declared := map[string]syntax.BashPPTypeExpr{}
	generics := map[string]*syntax.BashPPDecl{}
	ambiguous := map[string]bool{}
	if r.bashPPGoSourceFile != nil {
		syntax.Walk(r.bashPPGoSourceFile, func(node syntax.Node) bool {
			d, ok := node.(*syntax.BashPPDecl)
			if !ok || d.Site != syntax.StartTypeDecl || d.DeclTypeExpr == nil || d.Name == nil {
				return true
			}
			if len(d.TypeParams) == 0 {
				if _, exists := declared[d.Name.Value]; exists {
					ambiguous[d.Name.Value] = true
				}
				declared[d.Name.Value] = d.DeclTypeExpr
			} else if !d.Alias {
				if _, exists := generics[d.Name.Value]; exists {
					ambiguous[d.Name.Value] = true
				}
				generics[d.Name.Value] = d
			}
			return true
		})
	}
	for name := range ambiguous {
		delete(declared, name)
		delete(generics, name)
	}
	return &bashPPLocalTypeSet{declared: declared, imports: r.bashPPImports, generics: generics}
}

// bashPPImportedInstanceIdentity is the session identity contribution of the
// registered instantiations.
func bashPPImportedInstanceIdentity(list []bashPPImportedInstance) string {
	data, _ := json.Marshal(list)
	return string(data)
}

// bashPPImportedInstanceSymbols emits the helper's symbol entries for the
// instantiations of one imported generic function: name in the package
// imported under helperAlias, with typeParams type parameters. keyNames are
// the program's aliases for the package and aliases maps them to the
// helper's. An instantiation naming a local type the helper does not
// materialise is skipped; registering it would leave the helper unbuildable.
// It reports whether any entry was emitted.
func bashPPImportedInstanceSymbols(symbols *strings.Builder, list []bashPPImportedInstance, keyNames []string, name, helperAlias string, typeParams int, aliases map[string]string, materialised map[string]bool) (bool, error) {
	emitted := false
	for _, instance := range list {
		alias, symbol, ok := strings.Cut(instance.Selector, ".")
		if !ok || symbol != name || len(instance.Args) != typeParams {
			continue
		}
		bound := false
		for _, key := range keyNames {
			if key == alias {
				bound = true
			}
		}
		if !bound {
			continue
		}
		expressible := true
		for ref := range instance.refs {
			if !materialised[ref] {
				expressible = false
			}
		}
		if !expressible {
			continue
		}
		args := make([]string, len(instance.Args))
		for i, arg := range instance.Args {
			mapped, err := bashPPNativeTypeImports(arg, aliases)
			if err != nil {
				return false, err
			}
			args[i] = mapped
		}
		symbols.WriteString(strconv.Quote(instance.Key) + ": reflect.ValueOf(" + helperAlias + "." + name + "[" + strings.Join(args, ", ") + "]),\n")
		emitted = true
	}
	return emitted, nil
}
