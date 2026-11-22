// Package syncer contains the three-way merge that decides what to do
// when the PC and a phone meet. It is pure and fully unit tested.
//
// For every path we compare the PC state (L), the phone state (R) and the
// state both agreed on at the end of the last successful sync (B).
// Comparison is by content hash only.
package syncer

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CosmicKittu/Syncme/internal/index"
)

type Kind int

const (
	BaseSet    Kind = iota // already identical; just record it
	BaseRemove             // gone on both sides; forget it
	Pull                   // copy phone -> PC
	Push                   // copy PC -> phone
	DelLocal               // phone deleted it; delete on PC
	DelRemote              // PC deleted it; delete on phone
	// Both sides changed the file differently. The newer version keeps
	// the name; the other one is preserved as a conflict copy on both sides.
	ConflictKeepRemote // phone newer: rename PC copy, pull phone, push conflict copy
	ConflictKeepLocal  // PC newer: rename phone copy, push PC, pull conflict copy
)

func (k Kind) String() string {
	return [...]string{"base-set", "base-remove", "pull", "push", "del-local", "del-remote", "conflict-keep-remote", "conflict-keep-local"}[k]
}

type Action struct {
	Kind         Kind
	Path         string
	L, R         *index.Entry
	ConflictName string
}

func get(m map[string]index.Entry, p string) (*index.Entry, string) {
	if e, ok := m[p]; ok {
		return &e, e.Hash
	}
	return nil, ""
}

// Plan computes the actions. localName/remoteName are used for conflict
// file names (the name of the side whose copy is renamed).
func Plan(base, local, remote map[string]index.Entry, now time.Time, localName, remoteName string) []Action {
	paths := map[string]struct{}{}
	for _, m := range []map[string]index.Entry{base, local, remote} {
		for p := range m {
			paths[p] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	taken := func(p string) bool {
		_, a := local[p]
		_, b := remote[p]
		_, c := base[p]
		return a || b || c
	}
	reserved := map[string]bool{}

	var out []Action
	for _, p := range sorted {
		b, bh := get(base, p)
		l, lh := get(local, p)
		r, rh := get(remote, p)
		a := Action{Path: p, L: l, R: r}
		switch {
		case lh == rh:
			if lh == "" {
				if b == nil {
					continue
				}
				a.Kind = BaseRemove
			} else {
				if b != nil && bh == lh {
					continue // nothing changed at all
				}
				a.Kind = BaseSet
			}
		case lh == bh:
			if rh == "" {
				a.Kind = DelLocal
			} else {
				a.Kind = Pull
			}
		case rh == bh:
			if lh == "" {
				a.Kind = DelRemote
			} else {
				a.Kind = Push
			}
		case lh == "":
			a.Kind = Pull // deleted on PC but changed on phone: keep the change
		case rh == "":
			a.Kind = Push // deleted on phone but changed on PC: keep the change
		default:
			if r.MTime > l.MTime {
				a.Kind = ConflictKeepRemote
				a.ConflictName = conflictName(p, now, localName, taken, reserved)
			} else {
				a.Kind = ConflictKeepLocal
				a.ConflictName = conflictName(p, now, remoteName, taken, reserved)
			}
			reserved[a.ConflictName] = true
		}
		out = append(out, a)
	}
	return out
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func conflictName(p string, now time.Time, device string, taken func(string) bool, reserved map[string]bool) string {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if stem == "" { // dotfile such as ".bashrc"
		stem, ext = file, ""
	}
	dev := unsafeChars.ReplaceAllString(device, "")
	if len(dev) > 20 {
		dev = dev[:20]
	}
	if dev == "" {
		dev = "device"
	}
	baseName := stem + ".sync-conflict-" + now.Format("20060102-150405") + "-" + dev
	cand := dir + baseName + ext
	for i := 2; taken(cand) || reserved[cand]; i++ {
		cand = dir + baseName + "-" + itoa(i) + ext
	}
	return cand
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

// ValidateRemote drops invalid paths from a phone listing.
func ValidateRemote(files map[string]index.Entry) (bad []string) {
	for p, e := range files {
		if !index.ValidPath(p) || len(e.Hash) != 64 || e.Size < 0 {
			bad = append(bad, p)
			delete(files, p)
		}
	}
	return bad
}
