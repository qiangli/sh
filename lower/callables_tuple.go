package lower

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) operationFailure(name string) string {
	e.bridge = true
	if e.execution {
		return e.program() + ".Fail(" + name + ")"
	}
	return e.prefix + "rt.Fail(" + name + "); " + e.prefix + "rt.Status = " + e.prefix + "rt.ExitCode(" + name + ")"
}
func (e *emitter) tupleCallAssignment(n *syntax.BashPPAssign, rhs string) (string, error) {
	rt := e.prefix + "rt."
	var temps, targets, values []string
	for i, name := range n.Names {
		temp := fmt.Sprintf("%stuple%d", e.prefix, i)
		temps = append(temps, temp)
		values = append(values, rt+"TupleResult("+temp+")")
		target := "nil"
		if name.Value != "_" {
			target = "&" + e.goName(name.Value)
		}
		targets = append(targets, target)
	}
	failure := e.prefix + "tupleError"
	site := e.checkedValueSite(n, "")
	return "{\n" + strings.Join(temps, ",") + " := " + rhs + "\nif " + failure + " := " + rt + "AssignTuple([]any{" + strings.Join(targets, ",") + "}, []" + rt + "TupleValue{" + strings.Join(values, ",") + "}, " + site + "); " + failure + " != nil {" + e.operationFailure(failure) + "}\n}", nil
}
