package lower

import "strings"

// programEntrySource emits a hygienic private entry and the process main adapter.
// The compiler owns declaration-name collision checks and imports. A mixed unit
// imports e.prefix+"shellexec"; a typed-only unit never imports that backend.
func (e *emitter) programEntrySource(body string, mixedShell bool) string {
	return e.programEntrySourceNamed(body, mixedShell, e.prefix+"execute")
}

// programEntrySourceNamed implements an explicitly named injectable entry.
// The caller validates that an opt-in public name is an available exported Go
// identifier. The default private name is hygienic within the source unit.
func (e *emitter) programEntrySourceNamed(body string, mixedShell bool, entryName string) string {
	rt, p := e.prefix+"rt.", e.prefix+"program"
	var defaults []string
	// Session options override these per invocation. Do not use the support
	// package's legacy mutable process-wide output handles as entry defaults.
	defaults = append(defaults, rt+"WithStdio("+e.prefix+"os.Stdin,"+e.prefix+"os.Stdout,"+e.prefix+"os.Stderr)")
	// The process operands are the program's positional parameters, one
	// argument per parameter and byte for byte. It is a default like the
	// stdio triple: it is appended before the caller's opts, so an embedder
	// passing rt.WithParams(...) replaces it instead of being replaced by it.
	// os.Args is only read; WithParams copies what it is given.
	defaults = append(defaults, rt+"WithParams("+e.prefix+"os.Args[1:]...)")
	if mixedShell {
		defaults = append(defaults, rt+"WithShellFactory("+e.prefix+"shellexec.New("+e.prefix+"shellexec.BashPP()))")
	}
	return "// " + entryName + " runs a fresh runtime instance. Returned errors are not printed by the entry.\n" +
		"func " + entryName + "(opts ..." + rt + "SessionOption) (status int, err error) {\n" +
		"defaults := []" + rt + "SessionOption{" + strings.Join(defaults, ",") + "}\n" +
		p + ", err := " + rt + "NewProgram(append(defaults,opts...)...)\n" +
		"if err != nil { return " + rt + "ExitCode(err),err }\n" +
		"err = " + p + ".Run(func(" + p + " *" + rt + "Program) {\n" + body + "\n})\n" +
		"if err != nil { err = " + rt + "SourceFailure(err); return " + rt + "ExitCode(err),err }\n" +
		"return " + p + ".Status(),nil\n}\n" +
		"func main() {\nstatus,err := " + entryName + "()\n" +
		"if err != nil { " + e.prefix + "fmt.Fprintln(" + e.prefix + "os.Stderr,err) }\n" +
		"if status != 0 { " + e.prefix + "os.Exit(status) }\n}\n"
}
