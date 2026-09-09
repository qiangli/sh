package interp

import (
	"slices"

	"mvdan.cc/sh/v3/expand"
)

// GoSourceEnv supplies the original Go program's process environment in caller
// order. The slice is copied. It applies only to GoSource dependency processes;
// Bash variables and shell subprocess environments retain their usual behavior.
// An empty slice means an empty environment. Without this option, exported
// variables in the initial Runner.Env are used, in its iteration order.
// Reset retains this configuration; os.Setenv/Unsetenv in the Go program change
// its persistent dependency process only, not this starting snapshot.
func GoSourceEnv(env []string) RunnerOption {
	snapshot := append([]string{}, env...)
	return func(r *Runner) error { r.goSourceEnvironment = slices.Clone(snapshot); return nil }
}

func (r *Runner) bashPPGoSourceEnvironment() []string {
	if r.goSourceEnvironment != nil {
		return slices.Clone(r.goSourceEnvironment)
	}
	// A nonnil empty slice is essential: exec.Cmd interprets nil as inheritance
	// from the host process, which would leak unrelated embedding-process state.
	env := []string{}
	positions := make(map[string]int)
	if r.Env != nil {
		r.Env.Each(func(name string, value expand.Variable) bool {
			// Environ permits repeated names; the final occurrence determines
			// the value and export attribute, including an unset override.
			if previous, ok := positions[name]; ok {
				env[previous] = ""
				delete(positions, name)
			}
			if value.IsSet() && value.Exported {
				positions[name] = len(env)
				env = append(env, name+"="+value.String())
			}
			return true
		})
	}
	return slices.DeleteFunc(env, func(entry string) bool { return entry == "" })
}
