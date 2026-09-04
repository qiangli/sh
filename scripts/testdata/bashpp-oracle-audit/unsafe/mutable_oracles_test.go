// bashpp-racegate:audit
package fixture

import (
	"bytes"
	"strings"
)

var sharedResults = map[string]int{}
var sharedFlags []bool

func concurrentOracle() {
	var output bytes.Buffer
	var log strings.Builder
	go func() { output.WriteString(log.String()) }()
}
