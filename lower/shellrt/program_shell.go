package shellrt

// ShellExit stops generated statements after an explicit shell exit. It is a
// control transfer rather than a user panic or a second diagnostic.
type ShellExit struct{}

// ShellAbort carries a backend failure across native callable frames while
// retaining its identity for an injected entry's caller. It is not a source
// panic; generated source recover must preserve this control transfer.
type ShellAbort struct{ Err error }

func (p *Program) ShellRegion(source string) {
	if p.Frame.Agentic() {
		source = "agentic {\n" + source + "\n}"
	}
	if err := p.Session.Shell(p.Context, source); err != nil {
		panic(ShellAbort{Err: err})
	}
	p.SetStatus(p.Session.Status())
	if p.Session.Exited() {
		panic(ShellExit{})
	}
}
func (p *Program) ShellString(name string) string {
	value, _ := p.Session.Get(name)
	return value.String()
}
