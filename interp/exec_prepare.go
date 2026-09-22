package interp

import "strings"

// parseShebang implements bash's one-interpreter-argument shebang parsing.
func parseShebang(data []byte) (interp, optarg string, ok bool) {
	if len(data) < 2 || data[0] != '#' || data[1] != '!' {
		return "", "", false
	}
	line := string(data[2:])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSuffix(line, "\r")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}
	if len(fields) > 1 {
		optarg = fields[1]
	}
	return fields[0], optarg, true
}

func shebangArgs(interp, optarg, script string, args []string) []string {
	out := []string{interp}
	if optarg != "" {
		out = append(out, optarg)
	}
	out = append(out, script)
	if len(args) > 1 {
		out = append(out, args[1:]...)
	}
	return out
}
