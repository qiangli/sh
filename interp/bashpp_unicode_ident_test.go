// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPUnicodeIdentifiersRuntime(t *testing.T) {
	const src = `type 数据 struct { 值 int }
func 计算(参数 int) {
 结果２ := 参数 + 1
 结果２ = 结果２ + 1
 对象 := 数据{值: 7}
 字段 := 对象.值
 println(结果２, 字段)
}
计算(4)
`
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, src)
	qt.Assert(t, qt.Equals(out.String(), "6 7\n"))
}
