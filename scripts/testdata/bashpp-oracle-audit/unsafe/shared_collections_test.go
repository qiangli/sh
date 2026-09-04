// bashpp-racegate:audit
package fixture

var sharedMap = map[string]int{}
var sharedSlice []int

func shareCollections() {
	go func() { sharedMap["x"] = len(sharedSlice) }()
}
