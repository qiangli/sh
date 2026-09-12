package a

func F() any { return struct{ int }{0} }

func G() interface{} { return struct{ _ []int }{} }

func H() any { return struct{ N int }{7} }

var X = G()
