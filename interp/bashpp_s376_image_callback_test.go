//go:build full

package interp_test

import "testing"

// Sprint: #376; Story: #1507; Story-ID: 0cde6d446a94
//
// The shape of the Tour's solutions/image program: the PNG encoder calls the
// interpreted At method once per pixel, and every call returns a
// dependency-owned composite. 256 by 256 is the Tour's own size.
func TestGoSourceImageEncodeCallbacks(t *testing.T) {
	differGoSource(t, `package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

type Image struct {
	Height, Width int
}

func (m Image) ColorModel() color.Model {
	return color.RGBAModel
}

func (m Image) Bounds() image.Rectangle {
	return image.Rect(0, 0, m.Height, m.Width)
}

func (m Image) At(x, y int) color.Color {
	c := uint8(x ^ y)
	return color.RGBA{c, c, 255, 255}
}

func main() {
	m := Image{256, 256}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		panic(err)
	}
	fmt.Printf("%d %x\n", buf.Len(), sha256.Sum256(buf.Bytes()))
}
`, nil, "")
}

// A callback whose body outlasts the waiter's spin parks the waiter, which the
// reply must then wake; a reply too large for its mailbox slot travels on the
// control connection and may be what ends that park.
func TestGoSourceParkedCallbackWaiters(t *testing.T) {
	differGoSource(t, `package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type slow []int

func (s slow) Len() int      { return len(s) }
func (s slow) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s slow) Less(i, j int) bool {
	time.Sleep(2 * time.Millisecond)
	return s[i] < s[j]
}

type large struct {
	n     int
	delay time.Duration
}

func (l large) MarshalText() ([]byte, error) {
	time.Sleep(l.delay)
	return []byte(strings.Repeat("x", l.n)), nil
}

func main() {
	s := slow{5, 2, 8, 1, 9, 3}
	sort.Sort(s)
	fmt.Println(s)
	for _, l := range []large{{40 << 10, 0}, {40 << 10, 3 * time.Millisecond}, {7, 3 * time.Millisecond}} {
		b, err := json.Marshal(l)
		fmt.Println(len(b), err)
	}
}
`, nil, "")
}
