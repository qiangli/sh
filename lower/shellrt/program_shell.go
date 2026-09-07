package shellrt

// ShellExit stops generated statements after an explicit shell exit. It is a
// control transfer rather than a user panic or a second diagnostic.
type ShellExit struct{}

func (p *Program) ShellRegion(source string) {
	if p.Frame.Agentic() {
		source = "agentic {\n" + source + "\n}"
	}
	if err := p.Session.Shell(p.Context, source); err != nil {
		p.Fail(err)
		return
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
