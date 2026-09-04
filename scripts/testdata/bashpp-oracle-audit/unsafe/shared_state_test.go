// bashpp-racegate:audit
package fixture

var pollingFlag bool
var runnerState int

func pollState() {
	go func() { pollingFlag = runnerState > 0 }()
}
