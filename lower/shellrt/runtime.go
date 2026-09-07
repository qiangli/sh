// Package shellrt is the explicit shell boundary used by generated programs.
// The foundation supports scalar output; full dynamic shell state belongs to
// a separate extension of this boundary.
package shellrt

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var Stdout io.Writer = os.Stdout
var Stderr io.Writer = os.Stderr

func Word(v any) string { return fmt.Sprint(v) }

var Status int

func Fail(err error) { Status = 1; fmt.Fprintln(Stderr, err) }
func Exit() {
	if Status != 0 {
		os.Exit(Status)
	}
}
func Echo(args ...any) error {
	Status = 0
	a := make([]string, len(args))
	for i, v := range args {
		a[i] = Word(v)
	}
	_, err := fmt.Fprintln(Stdout, strings.Join(a, " "))
	return err
}

// Printf implements the foundation's %s, %d, %% and escape subset, including
// format recycling and omitted arguments. Unsupported conversions fail rather
// than quietly using Go fmt's different semantics.
func Printf(format string, args ...any) error {
	Status = 0
	var firstError error
	var output strings.Builder
	index := 0
	for {
		start := index
		for i := 0; i < len(format); i++ {
			c := format[i]
			if c == '\\' && i+1 < len(format) {
				i++
				switch format[i] {
				case 'n':
					output.WriteByte('\n')
				case 't':
					output.WriteByte('\t')
				case 'r':
					output.WriteByte('\r')
				case '\\':
					output.WriteByte('\\')
				case 'a':
					output.WriteByte('\a')
				case 'b':
					output.WriteByte('\b')
				case 'f':
					output.WriteByte('\f')
				case 'v':
					output.WriteByte('\v')
				case 'c':
					_, err := io.WriteString(Stdout, output.String())
					return err
				default:
					return fmt.Errorf("printf: unsupported escape \\%c", format[i])
				}
				continue
			}
			if c != '%' {
				output.WriteByte(c)
				continue
			}
			i++
			if i >= len(format) {
				return fmt.Errorf("printf: missing format character")
			}
			if format[i] == '%' {
				output.WriteByte('%')
				continue
			}
			if format[i] != 's' && format[i] != 'd' {
				return fmt.Errorf("printf: unsupported format character %c", format[i])
			}
			value := ""
			if index < len(args) {
				value = Word(args[index])
			}
			index++
			if format[i] == 's' {
				output.WriteString(value)
			} else {
				var n int64
				var err error
				if value != "" {
					n, err = strconv.ParseInt(value, 0, 64)
					if err != nil {
						if firstError == nil {
							firstError = fmt.Errorf("printf: %s: invalid number", value)
						}
						n = 0
					}
				}
				output.WriteString(strconv.FormatInt(n, 10))
			}
		}
		if index >= len(args) || index == start {
			break
		}
	}
	_, err := io.WriteString(Stdout, output.String())
	if err != nil {
		return err
	}
	return firstError
}
