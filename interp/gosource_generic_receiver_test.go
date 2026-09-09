package interp_test

import "testing"

// Sprint: #118; Story: #3; Story-ID: fa07603b71dc
func TestGoSourceGenericReceiverThreeModes(t *testing.T) {
	const source = `package main

import "fmt"

type List[T any] struct {
	next *List[T]
	val  T
}

func (l *List[T]) Push(v T) *List[T] {
	if l == nil {
		return &List[T]{val: v}
	}
	res := &List[T]{val: v}
	res.next = l
	return res
}

func main() {
	var l *List[int]
	l = l.Push(1)
	l = l.Push(2)
	l = l.Push(3)
	fmt.Println(l.val, l.next.val, l.next.next.val)
}
`
	typedSendThreeModes(t, source)
}
