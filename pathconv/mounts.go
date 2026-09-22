// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pathconv

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Mount maps one POSIX-spelled directory onto a native directory: Posix is
// the shell's spelling ("/usr/bin"), Native the host's ("C:\root\usr\bin").
type Mount struct {
	Posix  string
	Native string
}

// Mounts is the virtual POSIX root a Windows shell resolves absolute POSIX
// paths through. GNU Bash's own scripts hard-code /bin/sh, /usr/bin/printf,
// /etc/passwd and /tmp; on Windows those directories only exist inside a
// root laid out by the host (BASHY_ROOT), and every conversion in this
// package consults the mount table before falling back to the drive rules.
//
// Lookups are longest-prefix in both directions. The native side compares
// case-insensitively and treats / and \ alike, as NTFS does.
type Mounts struct {
	// Root is the native directory the POSIX root "/" maps to; empty when
	// only explicit mounts were given.
	Root string

	byPosix  []Mount // longest Posix first
	byNative []Mount // longest Native first, aliases excluded; ties prefer the longer Posix
}

// dirExists is the stat seam [NewMounts] uses to decide whether root\bin
// exists; tests pin it.
var dirExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// evalSymlinks is the seam [Discover] uses to canonicalize the root and temp
// directories (8.3 short names such as RUNNER~1 must not defeat FromOS once
// a physical cd has expanded them); it is left alone when it fails.
var evalSymlinks = filepath.EvalSymlinks

// NewMounts builds the mount table for a native root directory. The built-in
// entries are / -> root, /usr -> root\usr, /bin -> root\usr\bin (root\bin
// when that directory exists; otherwise /bin is an alias that [Mounts.FromOS]
// spells /usr/bin, like a merged-usr symlink), /etc -> root\etc,
// /tmp -> tempDir and /dev/null -> NUL. extra entries are added on top and override a built-in
// entry with the same Posix spelling. An empty root skips the root-derived
// entries; an empty tempDir skips /tmp.
func NewMounts(root string, extra []Mount, tempDir string) *Mounts {
	m := &Mounts{Root: root}
	table := make(map[string]string)
	// alias marks a POSIX name that is another entry's directory under a
	// second name (/bin on a merged-usr layout). It resolves forward but
	// never backward, the way a symlink behaves under pwd -P.
	alias := make(map[string]bool)
	if root != "" {
		root = strings.TrimRight(root, `\/`)
		if root == "" || (len(root) == 2 && root[1] == ':') {
			// "C:\" — keep the separator so the root stays absolute.
			root += `\`
		}
		m.Root = root
		table["/"] = root
		table["/usr"] = nativeJoin(root, "usr")
		if bin := nativeJoin(root, "bin"); dirExists(bin) {
			table["/bin"] = bin
		} else {
			table["/bin"] = nativeJoin(root, `usr\bin`)
			alias["/bin"] = true
		}
		table["/etc"] = nativeJoin(root, "etc")
	}
	if tempDir != "" {
		table["/tmp"] = tempDir
	}
	table["/dev/null"] = "NUL"
	for _, e := range extra {
		posix := cleanPosixMount(e.Posix)
		if posix == "" || e.Native == "" {
			continue
		}
		table[posix] = e.Native
		delete(alias, posix)
		if posix == "/" {
			m.Root = e.Native
		}
	}
	for posix, native := range table {
		m.byPosix = append(m.byPosix, Mount{Posix: posix, Native: native})
	}
	slices.SortFunc(m.byPosix, func(a, b Mount) int {
		if d := len(b.Posix) - len(a.Posix); d != 0 {
			return d
		}
		return strings.Compare(a.Posix, b.Posix)
	})
	for _, mt := range m.byPosix {
		if !alias[mt.Posix] {
			m.byNative = append(m.byNative, mt)
		}
	}
	slices.SortFunc(m.byNative, func(a, b Mount) int {
		if d := len(nativeKey(b.Native)) - len(nativeKey(a.Native)); d != 0 {
			return d
		}
		// Two explicit POSIX names on one native directory: the longer
		// spelling is the physical one.
		if d := len(b.Posix) - len(a.Posix); d != 0 {
			return d
		}
		return strings.Compare(a.Posix, b.Posix)
	})
	return m
}

// Discover builds the mount table from the environment: BASHY_ROOT names
// the native root directory and enables the built-in table; the optional
// BASHY_MOUNTS="<native>=<posix>;..." adds or overrides entries. Without
// BASHY_ROOT the result is nil, meaning the plain drive rules apply. No
// /etc/fstab is consulted.
func Discover(getenv func(string) string) *Mounts {
	root := getenv("BASHY_ROOT")
	if root == "" {
		return nil
	}
	root = canonicalNative(root)
	var extra []Mount
	for entry := range strings.SplitSeq(getenv("BASHY_MOUNTS"), ";") {
		native, posix, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		native, posix = strings.TrimSpace(native), strings.TrimSpace(posix)
		if native == "" || !strings.HasPrefix(posix, "/") {
			continue
		}
		extra = append(extra, Mount{Posix: posix, Native: canonicalNative(native)})
	}
	return NewMounts(root, extra, canonicalNative(TempDir()))
}

func canonicalNative(path string) string {
	if path == "" {
		return path
	}
	if resolved, err := evalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

var (
	currentMounts   atomic.Pointer[Mounts]
	mountsSet       atomic.Bool
	discoverMounts  sync.Once
	discoverEnviron = os.Getenv
)

// SetMounts installs the process-wide mount table consulted by [ToOS],
// [FromOS] and the other runtime conversions. Passing nil disables mounts
// and also suppresses the lazy [Discover].
func SetMounts(m *Mounts) {
	currentMounts.Store(m)
	mountsSet.Store(true)
}

// CurrentMounts returns the process-wide mount table. On Windows it is
// discovered from the environment on first use unless [SetMounts] ran
// first; on every other host it is always nil, so Unix conversions never
// change. It is package state rather than a Runner field because the
// interpreter's path helpers run in many places with no Runner in scope.
func CurrentMounts() *Mounts {
	if runtime.GOOS != "windows" {
		return nil
	}
	if !mountsSet.Load() {
		discoverMounts.Do(func() {
			if !mountsSet.Load() {
				currentMounts.Store(Discover(discoverEnviron))
				mountsSet.Store(true)
			}
		})
	}
	return currentMounts.Load()
}

// ToOS maps a POSIX-spelled absolute path through the mount table: the
// longest matching Posix prefix (the root "/" included) is replaced by its
// Native directory and the remainder is spelled with backslashes. ok is
// false when no mount applies or m is nil. Special characters in the
// remainder are not encoded; see [ToOSMountsMode] for the full conversion.
func (m *Mounts) ToOS(posix string) (string, bool) {
	native, rest, ok := m.lookupPosix(posix, true)
	if !ok {
		return "", false
	}
	return nativeJoin(native, strings.ReplaceAll(rest, "/", `\`)), true
}

// lookupPosix finds the longest mount whose Posix spelling is a prefix of
// posix on a component boundary, returning the Native directory and the
// remainder (without a leading slash). withRoot controls whether the "/"
// entry may match.
func (m *Mounts) lookupPosix(posix string, withRoot bool) (native, rest string, ok bool) {
	if m == nil || !strings.HasPrefix(posix, "/") {
		return "", "", false
	}
	for _, mt := range m.byPosix {
		if mt.Posix == "/" {
			if !withRoot {
				continue
			}
			return mt.Native, strings.TrimLeft(posix, "/"), true
		}
		if posix == mt.Posix {
			return mt.Native, "", true
		}
		if strings.HasPrefix(posix, mt.Posix) && posix[len(mt.Posix)] == '/' {
			return mt.Native, strings.TrimLeft(posix[len(mt.Posix):], "/"), true
		}
	}
	return "", "", false
}

// FromOS maps a native path back to its POSIX spelling through the longest
// mount whose Native directory is a prefix of it (case-insensitively, with
// / and \ interchangeable). ok is false when no mount applies or m is nil.
func (m *Mounts) FromOS(native string) (string, bool) {
	if m == nil || native == "" {
		return "", false
	}
	key := nativeKey(native)
	for _, mt := range m.byNative {
		mk := nativeKey(mt.Native)
		if mk == "" {
			continue
		}
		if len(key) < len(mk) || !strings.EqualFold(key[:len(mk)], mk) {
			continue
		}
		rest := key[len(mk):]
		if rest == "" {
			return mt.Posix, true
		}
		if rest[0] != '/' {
			continue
		}
		rest = strings.TrimLeft(rest, "/")
		if mt.Posix == "/" {
			return "/" + rest, true
		}
		if rest == "" {
			return mt.Posix, true
		}
		return mt.Posix + "/" + rest, true
	}
	return "", false
}

// nativeKey normalizes a native path for prefix comparison: forward
// slashes, no trailing separator ("C:\" becomes "C:").
func nativeKey(native string) string {
	k := strings.ReplaceAll(native, `\`, "/")
	return strings.TrimRight(k, "/")
}

func nativeJoin(dir, rest string) string {
	if rest == "" {
		return dir
	}
	if strings.HasSuffix(dir, `\`) || strings.HasSuffix(dir, "/") {
		return dir + rest
	}
	return dir + `\` + rest
}

func cleanPosixMount(posix string) string {
	if !strings.HasPrefix(posix, "/") {
		return ""
	}
	if p := strings.TrimRight(posix, "/"); p != "" {
		return p
	}
	return "/"
}

// SplitPathList splits a PATH-style list. On Windows the list is
// ';'-separated when it contains a ';', otherwise ':'-separated in the POSIX
// spelling ("/bin:/usr/bin"); a ':' split that cuts a native drive path
// ("c:/x:/bin" -> "c", "/x", "/bin") is re-joined. Off Windows the list is
// always ':'-separated. An empty list yields no elements.
func SplitPathList(v string, windows bool) []string {
	if v == "" {
		return []string{}
	}
	if !windows {
		return strings.Split(v, ":")
	}
	if strings.Contains(v, ";") {
		return strings.Split(v, ";")
	}
	parts := strings.Split(v, ":")
	out := parts[:0]
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		// A single-letter element means the split cut a native drive path
		// ("C:\x" -> "C", "\x"); glue it back onto the next element.
		if len(p) == 1 && isDriveLetter(p[0]) && i+1 < len(parts) {
			p += ":" + parts[i+1]
			i++
		}
		out = append(out, p)
	}
	return out
}

// NativePathMounts is [NativePath] extended with a mount table: besides the
// MSYS/WSL drive forms, an absolute POSIX path under a mount (/bin, /tmp/x,
// and any /foo once a root is mounted) becomes its native directory, with
// NTFS-forbidden characters in the remainder encoded as [EncodeSpecialMode]
// does. With a nil table it is exactly [NativePath].
func NativePathMounts(m *Mounts, value string) string {
	if len(value) < 2 || value[0] != '/' || strings.Contains(value, ":") {
		return value
	}
	// An explicit mount beats the drive forms, and the drive forms beat the
	// root mount, mirroring [ToOSMountsMode].
	if native, rest, ok := m.lookupPosix(value, false); ok {
		return mountNative(native, rest)
	}
	if drive, rest, ok := DrivePath(value); ok {
		return string(drive) + ":" + strings.ReplaceAll(rest, "/", `\`)
	}
	if native, rest, ok := m.lookupPosix(value, true); ok {
		return mountNative(native, rest)
	}
	return value
}

// mountNative spells the remainder of a mounted path natively under the
// mount's directory, encoding the characters NTFS refuses.
func mountNative(native, rest string) string {
	return nativeJoin(native, strings.ReplaceAll(EncodeShellRelativeMode(rest, true), "/", `\`))
}

// NativePathListMounts converts a path list into native form the way
// [NativePathList] does, mapping every element through [NativePathMounts]:
// a ':'-separated POSIX list becomes ';'-separated, elements under a mount
// become their native directory, and native elements stay as they are.
func NativePathListMounts(m *Mounts, value string) string {
	if value == "" {
		return value
	}
	elems := SplitPathList(value, true)
	for i, e := range elems {
		elems[i] = NativePathMounts(m, e)
	}
	return strings.Join(elems, ";")
}
