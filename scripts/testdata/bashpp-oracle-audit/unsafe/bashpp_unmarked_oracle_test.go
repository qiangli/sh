package fixture

import "bytes"

// No audit marker: the production manifest, not a filename/content heuristic,
// must still route a manifested Bash++ oracle through the scanner.
func concurrentBashPPOracle() {
	var output bytes.Buffer
	go func() { _, _ = output.WriteString("worker") }()
	_ = output.String()
}
