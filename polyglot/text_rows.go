package polyglot

// The built-in text fence rows. Each is data: the artifact's file name, the
// processor tool — resolved through [ToolResolver] when an embedder installs
// one, from PATH in a standalone engine — and the verb table with the
// effect atoms the contract layer reads (the atlas vocabulary: read, write,
// destroy, net, exec, spend, …). A row is the built-in answer to the same
// `methods` question a runner override answers at prepare.
//
// Verb arguments: `{file}` is the materialized artifact, `{dir}` its
// directory; the call's own string arguments follow.

func textRow(canonical string, aliases []string, text Text) Language {
	return Language{
		Canonical: canonical,
		Aliases:   aliases,
		NewRuntime: func(cfg RuntimeConfig) LanguageRuntime {
			t := text
			t.Dir, t.Environ = cfg.Dir, cfg.Environ
			return t
		},
		LoweredRuntime: func(prefix, _ string) string { return text.LoweredLiteral(prefix) },
	}
}

// Dockerfile is the `~~~dockerfile` row: `build` yields the image id (podman
// build -q), `run` runs an image the call names.
var Dockerfile = Text{Type: "dockerfile", FileName: "Dockerfile", Tool: "podman", Verbs: []Verb{
	{Name: "build", Args: []string{"build", "-q", "-f", "{file}", "{dir}"}, Effects: []string{"net", "write"}},
	{Name: "run", Args: []string{"run", "--rm"}, Effects: []string{"exec"}},
}}

// Tofu is the `~~~tf` row (aliases `tofu`, `hcl`): an OpenTofu module in
// one file. `init` and `plan` reach providers and state (net); `apply` and
// `destroy` change the world and are metered (spend); `validate` is local
// and carries nothing.
var Tofu = Text{Type: "tf", FileName: "main.tf", Tool: "tofu", Verbs: []Verb{
	{Name: "validate", Args: []string{"-chdir={dir}", "validate", "-no-color"}},
	{Name: "init", Args: []string{"-chdir={dir}", "init", "-input=false", "-no-color"}, Effects: []string{"net", "write"}},
	{Name: "plan", Args: []string{"-chdir={dir}", "plan", "-input=false", "-no-color"}, Effects: []string{"net", "read"}},
	{Name: "apply", Args: []string{"-chdir={dir}", "apply", "-auto-approve", "-input=false", "-no-color"}, Effects: []string{"net", "write", "spend"}},
	{Name: "destroy", Args: []string{"-chdir={dir}", "destroy", "-auto-approve", "-input=false", "-no-color"}, Effects: []string{"net", "destroy", "spend"}},
	{Name: "output", Args: []string{"-chdir={dir}", "output", "-no-color"}, Effects: []string{"read"}},
}}

func init() {
	RegisterLanguage(textRow("dockerfile", nil, Dockerfile))
	RegisterLanguage(textRow("tf", []string{"tofu", "hcl"}, Tofu))
}
