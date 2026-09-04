// bashpp-racegate:audit
package fixture

import (
	"bytes"
	"sync"
)

var sharedResults = map[string]int{} // bashpp-racegate:safe-synchronized

type lockedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer // bashpp-racegate:safe-synchronized
}

func concurrentOracle() {
	privateFlags := []bool{false} // bashpp-racegate:safe-private
	_ = privateFlags
	go func() {}()
}
